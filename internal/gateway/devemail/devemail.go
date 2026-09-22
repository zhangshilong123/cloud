// Package devemail is a Temporary Development Email Auth adapter — DEV ONLY.
//
// It is NOT part of the production authentication architecture. Production
// sign-in belongs to the Gateway + IDaaS/GitHub OAuth (see internal/gateway);
// this package exists so local development can exercise the real multi-user
// flows (register by email, log in as a different account, resolve internal
// users by email, add a workspace member by email) against the real core on
// PostgreSQL without an IDaaS tenant.
//
// It is wired only by development binaries (cmd/demo-issue-board-web), never by
// the production gateway, is toggled by an explicit switch (Enabled=false makes
// register/login unavailable), and can be deleted wholesale when dev moves to
// IDaaS. It deliberately reuses the existing identity tables (users,
// user_identities, tenant_memberships) and the core identity() resolution; it
// introduces no new tables and no second auth architecture.
//
// Fail-closed semantics:
//   - Register normalizes/lowercases/validates the email, rejects an already
//     registered address (409), creates the (dev-email, email) identity through
//     core identity resolution, joins the account to the dev tenant as a plain
//     member, and never auto-creates a Workspace (0 Workspace is a legal state).
//   - Login rejects an unknown address (401) — it never auto-creates a user.
//   - The adapter never trusts a client-supplied identity: every session is
//     resolved server-side from the (dev-email, email) identity row.
package devemail

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/wanglongan587/cloud/internal/core"
)

// Source is the identity-source namespace the adapter owns. Dev email identities
// live in user_identities as (source='dev-email', subject=<normalized email>),
// so add-by-email inside the same source resolves them like any other identity.
const Source = "dev-email"

// ServiceSubject is the gateway service subject the adapter binds user claims
// to (user.Caller must equal the service subject; see core.Claims).
const ServiceSubject = "dev-gateway"

// SessionCookie is the HttpOnly session cookie name for dev email sessions.
// It mirrors the production Gateway's HttpOnly cookie approach (never readable
// by frontend JS).
const SessionCookie = "ora_dev_session"

// Session is one signed-in dev email identity, kept only in memory. A dev
// bridge restart drops sessions (re-login is required), which is acceptable for
// a development-only adapter.
type Session struct {
	Token       string
	Subject     string // normalized email
	DisplayName string
	CreatedAt   time.Time
}

// Sessions is a small concurrency-safe in-memory session registry.
type Sessions struct {
	mu   sync.Mutex
	byID map[string]Session
}

// NewSessions returns an empty session registry.
func NewSessions() *Sessions { return &Sessions{byID: map[string]Session{}} }

func (s *Sessions) put(ses Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[ses.Token] = ses
}

func (s *Sessions) get(token string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ses, ok := s.byID[token]
	return ses, ok
}

func (s *Sessions) delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, token)
}

// Adapter serves the dev-only email auth endpoints and resolves sessions into
// the caller claims the dev bridge injects into every proxied request.
type Adapter struct {
	// Store is the real core store; user creation goes through core's own
	// identity() resolution and tenant membership uses the existing table.
	Store *core.Store
	// TenantID is the dev tenant every dev account joins (the bridge bootstraps it).
	TenantID string
	// Enabled gates register/login. When false both endpoints are 404, so "Dev
	// Auth OFF" makes the flows unavailable while leaving everything else intact.
	Enabled  bool
	Sessions *Sessions
}

// Register handles POST /auth/dev/register {name, email}.
func (a *Adapter) Register(w http.ResponseWriter, r *http.Request) {
	if !a.Enabled {
		writeFault(w, http.StatusNotFound, "dev_auth_disabled")
		return
	}
	var body struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeFault(w, http.StatusBadRequest, "invalid_json")
		return
	}
	email := normalizeEmail(body.Email)
	name := strings.TrimSpace(body.Name)
	if !validEmail(email) || name == "" || len(name) > 200 {
		writeFault(w, http.StatusBadRequest, "invalid_input")
		return
	}
	exists, err := a.identityExists(r.Context(), email)
	if err != nil {
		writeFault(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if exists {
		// No duplicate and no silent "log in as existing account" via register.
		writeFault(w, http.StatusConflict, "email_already_registered")
		return
	}
	// Create the account through the real core identity resolution (an
	// authenticated read self-registers the (dev-email, email) identity), then
	// join it to the dev tenant. 0 Workspaces is a legal state: registration
	// never auto-creates a Workspace.
	claims := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: email}, Source: Source, DisplayName: name, Caller: ServiceSubject}
	u, status, err := a.Store.Public(r.Context(), &core.PublicRequest{Method: "GET", Path: "/api/v1/me", Identity: &claims})
	if err != nil || status != 200 {
		writeFault(w, http.StatusInternalServerError, "internal_error")
		return
	}
	if err := a.ensureTenantMembership(r.Context(), u.S("id")); err != nil {
		writeFault(w, http.StatusInternalServerError, "internal_error")
		return
	}
	ses := newSession(email, name)
	a.Sessions.put(ses)
	setSessionCookie(w, ses.Token)
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

// Login handles POST /auth/dev/login {email}. The address must already be
// registered (identity row exists); unknown addresses are rejected with 401 and
// never auto-created.
func (a *Adapter) Login(w http.ResponseWriter, r *http.Request) {
	if !a.Enabled {
		writeFault(w, http.StatusNotFound, "dev_auth_disabled")
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeFault(w, http.StatusBadRequest, "invalid_json")
		return
	}
	email := normalizeEmail(body.Email)
	if !validEmail(email) {
		writeFault(w, http.StatusBadRequest, "invalid_input")
		return
	}
	var name string
	err := a.Store.Pool.QueryRowContext(r.Context(), `SELECT u.display_name FROM users u
JOIN user_identities i ON i.user_id=u.id
WHERE i.source=$1 AND i.subject=$2 AND u.status='active' AND u.deleted_at IS NULL`, Source, email).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		writeFault(w, http.StatusUnauthorized, "email_not_registered")
		return
	}
	if err != nil {
		writeFault(w, http.StatusInternalServerError, "internal_error")
		return
	}
	ses := newSession(email, name)
	a.Sessions.put(ses)
	setSessionCookie(w, ses.Token)
	writeJSON(w, http.StatusOK, map[string]any{"session": true})
}

// Logout handles POST /auth/logout: clears the dev session cookie. The
// production Gateway serves the same path for its sessions; the dev bridge
// serves it here for dev sessions.
func (a *Adapter) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookie); err == nil {
		a.Sessions.delete(cookie.Value)
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// Resolve returns the caller claims for the request's session cookie, if the
// cookie names a live session. It is the only place sessions become identity —
// the identity is never taken from the client.
func (a *Adapter) Resolve(r *http.Request) (core.Claims, bool) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		return core.Claims{}, false
	}
	ses, ok := a.Sessions.get(cookie.Value)
	if !ok {
		return core.Claims{}, false
	}
	return core.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: ses.Subject},
		Source:           Source,
		DisplayName:      ses.DisplayName,
		Caller:           ServiceSubject,
	}, true
}

// identityExists reports whether the (dev-email, email) identity row exists.
func (a *Adapter) identityExists(ctx context.Context, email string) (bool, error) {
	var one int
	err := a.Store.Pool.QueryRowContext(ctx, "SELECT 1 FROM user_identities WHERE source=$1 AND subject=$2", Source, email).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ensureTenantMembership joins the account to the dev tenant as an active plain
// member, keeping an existing role (e.g. admin) untouched. Idempotent.
func (a *Adapter) ensureTenantMembership(ctx context.Context, uid string) error {
	_, err := a.Store.Pool.ExecContext(ctx, "INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'member','active') ON CONFLICT (tenant_id,user_id) DO NOTHING", a.TenantID, uid)
	return err
}

// newSession creates an unguessable session token for the identity.
func newSession(subject, name string) Session {
	return Session{Token: uuid.NewString(), Subject: subject, DisplayName: name, CreatedAt: time.Now().UTC()}
}

// setSessionCookie writes the HttpOnly session cookie. Secure stays off because
// dev runs over plain HTTP on localhost; HttpOnly and Path=/ match the
// production Gateway convention.
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeFault(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"code": code, "params": map[string]any{}})
}

// validEmail is a deliberate lightweight registration check mirroring core's:
// a single '@' with non-empty local and domain parts and a dotted domain. It is
// not a full RFC 5322 validator — there is no mailbox delivery to be strict about.
func validEmail(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 512 {
		return false
	}
	local, domain, ok := strings.Cut(s, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") {
		return false
	}
	dot := strings.LastIndexByte(domain, '.')
	return dot > 0 && dot < len(domain)-1
}

// normalizeEmail folds an address to its canonical form (trimmed and lowercase)
// so case-variant duplicates collide on the single (source, subject) row —
// exactly as core does.
func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
