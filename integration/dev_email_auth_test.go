package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/gateway/devemail"
)

// devBridge wires the Temporary Dev Email Auth adapter in front of the real
// cloud router exactly like cmd/demo-issue-board-web does: the email auth routes
// are served directly, and everything else is proxied with dual-JWT injection
// resolved from the dev session cookie (falling back to the seeded demo user).
// `enabled` toggles the dev-auth switch.
func devBridge(t *testing.T, f *fixture, enabled bool) *httptest.Server {
	t.Helper()
	adapter := &devemail.Adapter{
		Store:    f.store,
		TenantID: f.tid,
		Enabled:  enabled,
		Sessions: devemail.NewSessions(),
	}
	fallback := f.user
	fallback.Caller = devemail.ServiceSubject
	target, e := url.Parse(f.cloud.URL)
	must(t, e)
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		claims, ok := adapter.Resolve(r)
		if !ok {
			claims = fallback
		}
		claims.Caller = devemail.ServiceSubject
		service, e := f.client.Credentials.Token("gateway", core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: devemail.ServiceSubject}})
		must(t, e)
		userToken, e := f.client.Credentials.Token("user", claims)
		must(t, e)
		r.Header.Set("Authorization", "Bearer "+service)
		r.Header.Set("X-Ora-User-Token", userToken)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/dev/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		adapter.Register(w, r)
	})
	mux.HandleFunc("/auth/dev/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		adapter.Login(w, r)
	})
	mux.HandleFunc("/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		adapter.Logout(w, r)
	})
	mux.Handle("/", proxy)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// bridgeClient returns an HTTP client with its own cookie jar so each dev
// account carries an independent session cookie.
func bridgeClient(srv *httptest.Server) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

// bridgePost issues one POST through the dev bridge. The router-facing writes
// need an Idempotency-Key; a fresh key per call keeps the tests independent.
func bridgePost(t *testing.T, client *http.Client, srv *httptest.Server, path string, body core.Object, want int) core.Object {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, e := http.NewRequest("POST", srv.URL+path, strings.NewReader(string(raw)))
	must(t, e)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", uuid.NewString())
	resp, e := client.Do(req)
	must(t, e)
	defer resp.Body.Close()
	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: want %d got %d: %s", path, want, resp.StatusCode, b)
	}
	if resp.StatusCode == http.StatusNoContent {
		return core.Object{}
	}
	var out core.Object
	must(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func bridgeGet(t *testing.T, client *http.Client, srv *httptest.Server, path string, want int) core.Object {
	t.Helper()
	resp, e := client.Get(srv.URL + path)
	must(t, e)
	defer resp.Body.Close()
	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: want %d got %d: %s", path, want, resp.StatusCode, b)
	}
	var out core.Object
	must(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

// TestDevEmailRegisterLoginLogout covers the full dev session lifecycle: a
// registered account resolves through /api/v1/me, and logout drops back to the
// demo fallback identity (post-clone preview preserved).
func TestDevEmailRegisterLoginLogout(t *testing.T) {
	f := setup(t)
	srv := devBridge(t, f, true)
	alice := bridgeClient(srv)

	out := bridgePost(t, alice, srv, "/auth/dev/register", core.Object{"name": "Alice Dev", "email": "Alice@Example.Test"}, 200)
	user := out.O("user")
	if user.S("displayName") != "Alice Dev" || user.S("id") == "" {
		t.Fatalf("register returned unexpected user: %v", out)
	}
	// The address is normalized to lowercase; the identity lives in user_identities.
	if f.scalar("SELECT count(*) FROM user_identities WHERE source='dev-email' AND subject='alice@example.test'") != 1 {
		t.Fatal("register did not create the normalized (dev-email, email) identity")
	}
	me := bridgeGet(t, alice, srv, "/api/v1/me", 200)
	if me.S("id") != user.S("id") || me.S("displayName") != "Alice Dev" {
		t.Fatalf("/api/v1/me did not resolve the dev session: %v", me)
	}
	// Login as the same account (no name needed) restores the session.
	another := bridgeClient(srv)
	bridgePost(t, another, srv, "/auth/dev/login", core.Object{"email": "alice@example.test"}, 200)
	meLogin := bridgeGet(t, another, srv, "/api/v1/me", 200)
	if meLogin.S("id") != user.S("id") {
		t.Fatalf("login resolved the wrong account: %v", meLogin)
	}
	// Logout clears the session; the next request falls back to the demo user.
	bridgePost(t, alice, srv, "/auth/logout", nil, 204)
	after := bridgeGet(t, alice, srv, "/api/v1/me", 200)
	if after.S("id") == user.S("id") {
		t.Fatalf("after logout /api/v1/me still resolved the dev account: %v", after)
	}
}

// TestDevEmailRegisterRejectsDuplicate covers §14: an already-registered address
// is 409 email_already_registered — never a duplicate and never a silent login.
func TestDevEmailRegisterRejectsDuplicate(t *testing.T) {
	f := setup(t)
	srv := devBridge(t, f, true)
	c := bridgeClient(srv)
	bridgePost(t, c, srv, "/auth/dev/register", core.Object{"name": "Alice", "email": "alice@example.test"}, 200)
	dup := bridgePost(t, c, srv, "/auth/dev/register", core.Object{"name": "Alice Two", "email": "ALICE@example.test"}, 409)
	if dup.S("code") != "email_already_registered" {
		t.Fatalf("duplicate register: want email_already_registered got %v", dup)
	}
	if f.scalar("SELECT count(*) FROM user_identities WHERE source='dev-email'") != 1 {
		t.Fatal("duplicate register created a second identity")
	}
}

// TestDevEmailLoginUnknownRejected covers §15: an unknown address is 401 and
// never auto-created — login does not register.
func TestDevEmailLoginUnknownRejected(t *testing.T) {
	f := setup(t)
	srv := devBridge(t, f, true)
	c := bridgeClient(srv)
	out := bridgePost(t, c, srv, "/auth/dev/login", core.Object{"email": "nobody@example.test"}, 401)
	if out.S("code") != "email_not_registered" {
		t.Fatalf("login unknown: want email_not_registered got %v", out)
	}
	if f.scalar("SELECT count(*) FROM user_identities WHERE source='dev-email'") != 0 {
		t.Fatal("login unknown auto-created an identity")
	}
}

// TestDevEmailRegisterTenantMembershipNoWorkspace covers §14: registration joins
// the account to the dev tenant as a member but never auto-creates a Workspace
// — 0 Workspace is a legal, usable state.
func TestDevEmailRegisterTenantMembershipNoWorkspace(t *testing.T) {
	f := setup(t)
	srv := devBridge(t, f, true)
	c := bridgeClient(srv)
	out := bridgePost(t, c, srv, "/auth/dev/register", core.Object{"name": "Bob", "email": "bob@example.test"}, 200)
	uid := out.O("user").S("id")
	if f.scalar("SELECT count(*) FROM tenant_memberships WHERE tenant_id=$1 AND user_id=$2 AND role='member' AND status='active'", f.tid, uid) != 1 {
		t.Fatal("register did not join the account to the dev tenant as a member")
	}
	if f.scalar("SELECT count(*) FROM collab_workspace_members WHERE user_id=$1", uid) != 0 {
		t.Fatal("register auto-created a workspace membership; 0 Workspace must be legal")
	}
	tenants := bridgeGet(t, c, srv, "/api/v1/me/tenants", 200)
	if len(tenants["items"].([]any)) != 1 {
		t.Fatalf("registered account should see exactly the dev tenant: %v", tenants)
	}
}

// TestDevEmailRegisterInvalidInput covers the fail-closed input guards.
func TestDevEmailRegisterInvalidInput(t *testing.T) {
	f := setup(t)
	srv := devBridge(t, f, true)
	c := bridgeClient(srv)
	bridgePost(t, c, srv, "/auth/dev/register", core.Object{"name": "X", "email": "not-an-email"}, 400)
	bridgePost(t, c, srv, "/auth/dev/register", core.Object{"name": "", "email": "a@b.co"}, 400)
	bridgePost(t, c, srv, "/auth/dev/login", core.Object{"email": "not-an-email"}, 400)
	if f.scalar("SELECT count(*) FROM user_identities WHERE source='dev-email'") != 0 {
		t.Fatal("invalid register input created an identity")
	}
}

// TestDevEmailAuthDisabled covers §18: with the switch off, register and login
// are unavailable (404) and nothing is created.
func TestDevEmailAuthDisabled(t *testing.T) {
	f := setup(t)
	srv := devBridge(t, f, false)
	c := bridgeClient(srv)
	out := bridgePost(t, c, srv, "/auth/dev/register", core.Object{"name": "A", "email": "a@example.test"}, 404)
	if out.S("code") != "dev_auth_disabled" {
		t.Fatalf("disabled register: want dev_auth_disabled got %v", out)
	}
	bridgePost(t, c, srv, "/auth/dev/login", core.Object{"email": "a@example.test"}, 404)
	if f.scalar("SELECT count(*) FROM user_identities WHERE source='dev-email'") != 0 {
		t.Fatal("disabled dev auth created an identity")
	}
}

// TestDevEmailMultiUserAddByEmail is the Regression 1 end-to-end proof: Alice
// registers, creates a workspace, adds Bob by email (case-variant), and Bob —
// logged into his own dev session — sees the shared workspace.
func TestDevEmailMultiUserAddByEmail(t *testing.T) {
	f := setup(t)
	srv := devBridge(t, f, true)
	alice := bridgeClient(srv)
	bob := bridgeClient(srv)

	bridgePost(t, alice, srv, "/auth/dev/register", core.Object{"name": "Alice Dev", "email": "alice@example.test"}, 200)
	bridgePost(t, bob, srv, "/auth/dev/register", core.Object{"name": "Bob Dev", "email": "bob@example.test"}, 200)

	space := bridgePost(t, alice, srv, f.path("/spaces"), core.Object{"name": "Dev Team", "slug": "dev-team"}, 200)
	sid := space.S("id")
	if sid == "" {
		t.Fatalf("workspace create returned no id: %v", space)
	}
	member := bridgePost(t, alice, srv, f.path("/spaces/"+sid+"/members"), core.Object{"email": "BOB@example.test"}, 200)
	if member.S("role") != "member" || member.S("status") != "active" {
		t.Fatalf("unexpected member row: %v", member)
	}

	bridgePost(t, bob, srv, "/auth/dev/login", core.Object{"email": "bob@example.test"}, 200)
	spaces := bridgeGet(t, bob, srv, f.path("/spaces"), 200)
	found := false
	for _, item := range spaces["items"].([]any) {
		if core.Object(item.(map[string]any)).S("id") == sid {
			found = true
		}
	}
	if !found {
		t.Fatalf("bob does not see the shared workspace: %v", spaces)
	}
}

// TestDevEmailSessionCollaborationTargetsAndForms proves Regressions 2+3 are
// restored through the dev bridge: the @ picker catalog includes
// user/agent/team/workflow, and the workflow form descriptor resolves.
func TestDevEmailSessionCollaborationTargetsAndForms(t *testing.T) {
	f := setup(t)
	srv := devBridge(t, f, true)
	c := bridgeClient(srv)
	bridgePost(t, c, srv, "/auth/dev/register", core.Object{"name": "Dev User", "email": "dev@example.test"}, 200)

	targets := bridgeGet(t, c, srv, f.path("/collaboration/targets"), 200)
	types := map[string]bool{}
	for _, item := range targets["items"].([]any) {
		types[core.Object(item.(map[string]any)).S("type")] = true
	}
	for _, want := range []string{"user", "agent", "team", "workflow"} {
		if !types[want] {
			t.Fatalf("collaboration targets missing type %q: %v", want, targets)
		}
	}

	form := bridgeGet(t, c, srv, f.path("/collaboration/forms/security-review"), 200)
	if form.S("formRef") != "security-review" {
		t.Fatalf("workflow form descriptor unexpected: %v", form)
	}
	if fields, ok := form["fields"].([]any); !ok || len(fields) == 0 {
		t.Fatalf("workflow form descriptor has no fields: %v", form)
	}
}
