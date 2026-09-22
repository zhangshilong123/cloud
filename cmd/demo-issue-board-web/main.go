// Command demo-issue-board-web runs the issue-board Kanban UI in a browser, backed by the
// real Cloud HTTP API (router.New) on PostgreSQL 17. It provisions an isolated schema and a
// demo tenant, signs the dual-JWT credentials server-side (the browser stays untrusted), and
// serves the single-file UI and the /api/v1/... endpoints from one origin so no CORS is needed.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/wanglongan587/cloud/internal/api/router"
	"github.com/wanglongan587/cloud/internal/collab"
	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/gateway/devemail"
	"github.com/wanglongan587/cloud/internal/simulator"
)

//go:embed index.html
var indexHTML []byte

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "demo failed:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	dsn := os.Getenv("DEMO_DATABASE_URL")
	if dsn == "" {
		dsn = "host=127.0.0.1 port=55432 user=ora password=ora-local dbname=ora sslmode=disable"
	}
	config, e := pgx.ParseConfig(dsn)
	if e != nil {
		return e
	}
	admin := stdlib.OpenDB(*config)
	if e = admin.Ping(); e != nil {
		return fmt.Errorf("connect PostgreSQL: %w", e)
	}
	defer admin.Close()

	// Isolated schema keeps the demo away from any real data and makes rollback trivial.
	schema := "demo_issues_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec("CREATE SCHEMA " + schema); e != nil {
		return e
	}
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	config.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*config)
	defer pool.Close()
	db, e := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		return e
	}
	store, e := core.NewStore(db)
	if e != nil {
		return e
	}
	if e = store.Migrate(ctx); e != nil {
		return fmt.Errorf("migrate: %w", e)
	}

	// Wire the dev collaboration fixtures (agents/teams/workflows, form
	// descriptors, deterministic context, mock execution) so the Issue @ picker
	// and the workflow form are usable in the demo. This restores what the
	// pre-merge cmd/ora-web did; production servers leave these ports nil
	// ("Unavailable") and wire real adapters instead.
	store.Directory = collab.FixtureCollaborationDirectory{}
	store.Context = collab.DeterministicContextBuilder{}
	store.Dispatcher = collab.MockExecutionDispatcher{}
	store.Forms = collab.FixtureFormDescriptorProvider{}
	store.Assist = collab.MockInputAssistProvider{}

	credentials, e := simulator.NewCredentials()
	if e != nil {
		return e
	}
	auth, e := core.NewAuthenticator("ora-cloud", credentials.Trust)
	if e != nil {
		return e
	}
	gin.SetMode(gin.ReleaseMode)
	log, _ := zap.NewProduction()
	real := router.New(store, auth, log)

	bootstrap, e := store.Bootstrap(ctx, "Demo Board", "demo", "alice", "Alice")
	if e != nil {
		return e
	}
	tid := bootstrap.S("tenantId")

	// Temporary Development Email Auth (dev-only; see internal/gateway/devemail).
	// Enables register-by-email / login-as-a-different-account / add-by-email in
	// local dev. Enabled by default; DEMO_DEV_AUTH=0 makes register/login 404.
	// The production gateway never mounts this adapter.
	devAuth := &devemail.Adapter{
		Store:    store,
		TenantID: tid,
		Enabled:  os.Getenv("DEMO_DEV_AUTH") != "0",
		Sessions: devemail.NewSessions(),
	}

	// The two identities the browser acts under: the gateway (service) and the
	// demo user. Service subject matches the devemail adapter so dev sessions
	// and the fallback demo user both bind to the same service identity.
	gateway := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: devemail.ServiceSubject}}
	alice := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "alice"}, Source: "demo", DisplayName: "Alice", Caller: devemail.ServiceSubject}

	// Session-aware injection: a live dev email session resolves to that account;
	// otherwise the request falls back to the seeded demo user (alice) so a fresh
	// browser is immediately usable (post-clone preview).
	inject := func(r *http.Request, user *core.Claims) error {
		gw, e := credentials.Token("gateway", gateway)
		if e != nil {
			return e
		}
		usr, e := credentials.Token("user", *user)
		if e != nil {
			return e
		}
		r.Header.Set("Authorization", "Bearer "+gw)
		r.Header.Set("X-Ora-User-Token", usr)
		return nil
	}
	resolve := func(r *http.Request) core.Claims {
		if claims, ok := devAuth.Resolve(r); ok {
			return claims
		}
		return alice
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/dev/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		devAuth.Register(w, r)
	})
	mux.HandleFunc("/auth/dev/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		devAuth.Login(w, r)
	})
	mux.HandleFunc("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		devAuth.Logout(w, r)
	})
	mux.HandleFunc("/demo/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		claims := resolve(r)
		b, _ := json.Marshal(demoConfig(r.Context(), store, tid, &claims))
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			claims := resolve(r)
			if e := inject(r, &claims); e != nil {
				http.Error(w, e.Error(), http.StatusInternalServerError)
				return
			}
			real.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The embedded HTML changes between demo restarts; forbid heuristic caching so a
		// reopened tab never runs a stale script against the new tenant.
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(indexHTML)
	})

	addr := os.Getenv("DEMO_WEB_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8899"
	}
	ln, e := net.Listen("tcp", addr)
	if e != nil {
		ln, e = net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			return e
		}
	}
	url := "http://" + ln.Addr().String() + "/"
	fmt.Printf("Issue Board UI: %s\n", url)
	fmt.Println("Press Ctrl+C to stop (the demo schema is dropped on exit).")
	if open := os.Getenv("DEMO_WEB_OPEN"); open == "" || open == "1" {
		_ = openBrowser(url)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	srv := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if e = srv.Serve(ln); e != nil && e != http.ErrServerClosed {
		return e
	}
	return nil
}

func openBrowser(url string) error {
	return exec.Command("cmd", "/c", "start", "", url).Start()
}

// demoConfig assembles the live status catalog and label list straight from the store so the UI
// reflects custom columns and labels created during the demo session. The browser stays untrusted:
// these reads are server-side calls to the same core used by the public API.
func demoConfig(ctx context.Context, store *core.Store, tid string, user *core.Claims) map[string]any {
	me, _, _ := store.Public(ctx, &core.PublicRequest{Method: "GET", Path: "/api/v1/me", Identity: user})
	statuses, _, _ := store.Public(ctx, &core.PublicRequest{Method: "GET", Path: "/api/v1/tenants/" + tid + "/issue-statuses", TenantID: tid, Identity: user})
	labels, _, _ := store.Public(ctx, &core.PublicRequest{Method: "GET", Path: "/api/v1/tenants/" + tid + "/labels", TenantID: tid, Identity: user})
	members, _, _ := store.Public(ctx, &core.PublicRequest{Method: "GET", Path: "/api/v1/tenants/" + tid + "/members", TenantID: tid, Identity: user})

	statusList := []map[string]any{}
	if items, ok := statuses["items"].([]core.Object); ok {
		for _, s := range items {
			statusList = append(statusList, map[string]any{
				"key":      s.S("key"),
				"name":     s.S("name"),
				"color":    s.S("color"),
				"isSystem": s.B("isSystem"),
			})
		}
	}
	labelList := []map[string]any{}
	if items, ok := labels["items"].([]core.Object); ok {
		for _, l := range items {
			labelList = append(labelList, map[string]any{"id": l.S("id"), "name": l.S("name"), "color": l.S("color")})
		}
	}
	memberList := []map[string]any{}
	if items, ok := members["items"].([]core.Object); ok {
		for _, m := range items {
			memberList = append(memberList, map[string]any{"id": m.S("userId"), "displayName": m.S("displayName"), "role": m.S("role")})
		}
	}
	return map[string]any{
		"tenantId":   tid,
		"user":       map[string]any{"id": me.S("id"), "subject": user.Subject, "displayName": user.DisplayName},
		"statuses":   statusList,
		"labels":     labelList,
		"members":    memberList,
		"priorities": []string{"urgent", "high", "medium", "low", "none"},
	}
}
