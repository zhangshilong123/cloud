package integration

// Public Skill source upload (Step 3B) end-to-end tests. These drive the real HTTP route
// POST /api/v1/tenants/:tid/spaces/:spaceId/skills/imports through router.New with dual
// gateway+user credentials and an in-memory fakestore.ObjectStore, pinning the obligations from
// specs/decisions/cloud/skills/20260927-public-upload-api-contract.md:
//
//   - happy path for zip and tar returns a 200 { sourceKind, preparationFailures[], ingestions[] }
//     envelope with committed/activated results and resolved identity fields;
//   - the same Idempotency-Key + same source replays without new rows;
//   - the same Idempotency-Key + same canonical_name + different content is a 409
//     idempotency_conflict;
//   - a non-admin member is 403 workspace_admin_required, a non-member is 404 not_found, and a
//     target_skill_id resolving to another workspace is 404 not_found (no existence leak);
//   - target_skill_id with more than one candidate is 400 single_candidate_required;
//   - a partially valid source maps preparationFailures and per-candidate ingestions in order;
//   - a structurally fatal source is a 400 source_* with zero side effects;
//   - a body larger than the 64 KiB global JSON ceiling is accepted on this route alone.

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/wanglongan587/cloud/internal/api/router"
	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/simulator"
	"github.com/wanglongan587/cloud/internal/skillstore/fakestore"
)

// buildSourceTar builds an uncompressed TAR archive from path→content entries.
func buildSourceTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

// skillUploadFixture provisions one tenant with three workspaces-collaborators and a live HTTP
// server for the public upload route. The actor identities resolve through user_identities:
// alice is an owner of ws1, bob an ordinary member of ws1, carol a non-member of ws1, and ws2 is
// a second workspace for cross-workspace target scoping.
type skillUploadFixture struct {
	t        *testing.T
	store    *core.Store
	creds    *simulator.Credentials
	server   *httptest.Server
	tid      string
	ws1, ws2 string
	ownerUID string
}

func newSkillUploadFixture(t *testing.T) *skillUploadFixture {
	t.Helper()
	pool, _ := testSchema(t, "skill_upload_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	owner, tenant, ws1, ws2 := seedSkillBase(t, pool)

	// Owner of ws1 resolves from identity (corp, alice).
	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','alice')`, owner)
	execOK(t, pool, `INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'owner','active',$2)`, ws1, owner)

	// Ordinary member of ws1 (bob) — active tenant membership + member role in ws1.
	member := uuid.NewString()
	execOK(t, pool, `INSERT INTO users(id,display_name,status) VALUES($1,'Bob','active')`, member)
	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','bob')`, member)
	execOK(t, pool, `INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'member','active')`, tenant, member)
	execOK(t, pool, `INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'member','active',$3)`, ws1, member, owner)

	// Non-member of ws1 (carol) — active tenant membership but no workspace membership.
	outsider := uuid.NewString()
	execOK(t, pool, `INSERT INTO users(id,display_name,status) VALUES($1,'Carol','active')`, outsider)
	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','carol')`, outsider)
	execOK(t, pool, `INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'member','active')`, tenant, outsider)

	store.SkillsObjectStore = &fakestore.Fake{}
	creds, err := simulator.NewCredentials()
	must(t, err)
	auth, err := core.NewAuthenticator("ora-cloud", creds.Trust)
	must(t, err)
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(router.New(store, auth, zap.NewNop()))
	t.Cleanup(server.Close)

	return &skillUploadFixture{t: t, store: store, creds: creds, server: server, tid: tenant, ws1: ws1, ws2: ws2, ownerUID: owner}
}

// uploadSpec describes one multipart upload: which caller, what kind/source, and which optional
// fields. omitKey/omitSource let a test drive the missing-field branches without a body.
type uploadSpec struct {
	subject     string
	kind        string
	source      []byte
	key         string
	spaceID     string
	target      string
	displayName string
	summary     string
	extraFields map[string]string
	omitKey     bool
	omitSource  bool
}

// upload performs the multipart POST with dual credentials and returns the HTTP status plus the
// decoded JSON body (nil when the body is empty).
func (f *skillUploadFixture) upload(spec uploadSpec) (int, map[string]any) {
	f.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	field := func(name, value string) {
		if err := mw.WriteField(name, value); err != nil {
			f.t.Fatalf("write field %s: %v", name, err)
		}
	}
	if spec.kind != "" {
		field("source_kind", spec.kind)
	}
	if spec.target != "" {
		field("target_skill_id", spec.target)
	}
	if spec.displayName != "" {
		field("display_name", spec.displayName)
	}
	if spec.summary != "" {
		field("summary", spec.summary)
	}
	for k, v := range spec.extraFields {
		field(k, v)
	}
	if !spec.omitSource {
		fw, err := mw.CreateFormFile("source", "source.bin")
		if err != nil {
			f.t.Fatalf("create source part: %v", err)
		}
		if _, err := fw.Write(spec.source); err != nil {
			f.t.Fatalf("write source part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		f.t.Fatalf("close multipart: %v", err)
	}

	spaceID := spec.spaceID
	if spaceID == "" {
		spaceID = f.ws1
	}
	url := f.server.URL + "/api/v1/tenants/" + f.tid + "/spaces/" + spaceID + "/skills/imports"
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	must(f.t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	svc, err := f.creds.Token("gateway", core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}})
	must(f.t, err)
	req.Header.Set("Authorization", "Bearer "+svc)
	usr, err := f.creds.Token("user", core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: spec.subject}, Source: "corp", DisplayName: "Caller", Caller: "gateway-a"})
	must(f.t, err)
	req.Header.Set("X-Ora-User-Token", usr)
	if !spec.omitKey {
		key := spec.key
		if key == "" {
			key = "key"
		}
		req.Header.Set("Idempotency-Key", key)
	}

	resp, err := http.DefaultClient.Do(req)
	must(f.t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	must(f.t, err)
	var out map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			f.t.Fatalf("decode %q: %v", body, err)
		}
	}
	return resp.StatusCode, out
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func (f *skillUploadFixture) skillRowCounts() (skills, revisions, ingestions int) {
	f.t.Helper()
	must(f.t, f.store.Pool.QueryRow(`SELECT (SELECT count(*) FROM skills), (SELECT count(*) FROM skill_revisions), (SELECT count(*) FROM skill_ingestions)`).Scan(&skills, &revisions, &ingestions))
	return
}

func TestSkillUploadZipHappyPath(t *testing.T) {
	f := newSkillUploadFixture(t)
	src := buildSourceZip(t, map[string]string{
		"a/SKILL.md": srcMD("alpha", "desc", "body\n"),
		"a/guide.md": "read me",
	})

	status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: src, key: "zip-1"})
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d (%v)", status, body)
	}
	if body["sourceKind"] != "zip" {
		t.Fatalf("sourceKind: want zip, got %v", body["sourceKind"])
	}
	ings := asSlice(body["ingestions"])
	if len(ings) != 1 {
		t.Fatalf("want 1 ingestion, got %d", len(ings))
	}
	ing := asMap(ings[0])
	if ing["canonicalName"] != "alpha" || ing["state"] != "committed" || ing["activation"] != "activated" {
		t.Fatalf("unexpected ingestion: %v", ing)
	}
	if ing["skillId"] == nil || ing["revisionId"] == nil || ing["ingestionId"] == nil {
		t.Fatalf("identity fields must resolve: %v", ing)
	}
	if ing["replayed"] != false {
		t.Fatalf("first upload must not replay: %v", ing)
	}
	if skills, revisions, ingestions := f.skillRowCounts(); skills != 1 || revisions != 1 || ingestions != 1 {
		t.Fatalf("want 1/1/1 rows, got %d/%d/%d", skills, revisions, ingestions)
	}
}

func TestSkillUploadTarHappyPath(t *testing.T) {
	f := newSkillUploadFixture(t)
	src := buildSourceTar(t, map[string]string{
		"a/SKILL.md": srcMD("alpha", "desc", "body\n"),
		"a/guide.md": "read me",
	})

	status, body := f.upload(uploadSpec{subject: "alice", kind: "tar", source: src, key: "tar-1"})
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d (%v)", status, body)
	}
	if body["sourceKind"] != "tar" {
		t.Fatalf("sourceKind: want tar, got %v", body["sourceKind"])
	}
	ings := asSlice(body["ingestions"])
	if len(ings) != 1 || asMap(ings[0])["state"] != "committed" || asMap(ings[0])["activation"] != "activated" {
		t.Fatalf("unexpected ingestions: %v", ings)
	}
}

func TestSkillUploadReplaysSameKeyAndSource(t *testing.T) {
	f := newSkillUploadFixture(t)
	src := buildSourceZip(t, map[string]string{"a/SKILL.md": srcMD("alpha", "", "body\n"), "a/guide.md": "r"})

	if status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: src, key: "K"}); status != http.StatusOK {
		t.Fatalf("first upload: %d (%v)", status, body)
	}
	status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: src, key: "K"})
	if status != http.StatusOK {
		t.Fatalf("replay: want 200, got %d (%v)", status, body)
	}
	ing := asMap(asSlice(body["ingestions"])[0])
	if ing["replayed"] != true {
		t.Fatalf("replay must set replayed=true: %v", ing)
	}
	if skills, revisions, ingestions := f.skillRowCounts(); skills != 1 || revisions != 1 || ingestions != 1 {
		t.Fatalf("replay must not create rows: %d/%d/%d", skills, revisions, ingestions)
	}
}

func TestSkillUploadConflictOnDifferentFingerprint(t *testing.T) {
	f := newSkillUploadFixture(t)
	one := buildSourceZip(t, map[string]string{"a/SKILL.md": srcMD("alpha", "", "one\n"), "a/guide.md": "r"})
	two := buildSourceZip(t, map[string]string{"a/SKILL.md": srcMD("alpha", "", "two\n"), "a/guide.md": "r"})

	if status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: one, key: "K"}); status != http.StatusOK {
		t.Fatalf("first upload: %d (%v)", status, body)
	}
	status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: two, key: "K"})
	if status != http.StatusConflict {
		t.Fatalf("want 409, got %d (%v)", status, body)
	}
	if body["code"] != "idempotency_conflict" {
		t.Fatalf("want idempotency_conflict, got %v", body["code"])
	}
}

func TestSkillUploadMemberForbidden(t *testing.T) {
	f := newSkillUploadFixture(t)
	src := buildSourceZip(t, map[string]string{"a/SKILL.md": srcMD("alpha", "", "body\n"), "a/guide.md": "r"})
	status, body := f.upload(uploadSpec{subject: "bob", kind: "zip", source: src, key: "K"})
	if status != http.StatusForbidden || body["code"] != "workspace_admin_required" {
		t.Fatalf("want 403 workspace_admin_required, got %d (%v)", status, body)
	}
	if skills, _, _ := f.skillRowCounts(); skills != 0 {
		t.Fatalf("forbidden upload must be side-effect free, got %d skills", skills)
	}
}

func TestSkillUploadNonMemberNotFound(t *testing.T) {
	f := newSkillUploadFixture(t)
	src := buildSourceZip(t, map[string]string{"a/SKILL.md": srcMD("alpha", "", "body\n"), "a/guide.md": "r"})
	status, body := f.upload(uploadSpec{subject: "carol", kind: "zip", source: src, key: "K"})
	if status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("want 404 not_found, got %d (%v)", status, body)
	}
}

func TestSkillUploadCrossWorkspaceTargetNotFound(t *testing.T) {
	f := newSkillUploadFixture(t)
	target := insertSkill(t, f.store.Pool, f.ws2, "beta", f.ownerUID)
	src := buildSourceZip(t, map[string]string{"a/SKILL.md": srcMD("alpha", "", "body\n"), "a/guide.md": "r"})

	status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: src, key: "K", target: target})
	if status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("cross-workspace target must 404 not_found, got %d (%v)", status, body)
	}
	if skills, _, _ := f.skillRowCounts(); skills != 1 {
		t.Fatalf("cross-workspace target must not create a skill in ws1, got %d", skills)
	}

	// An absent target (valid UUID, no row) is also 404, never a 200 envelope.
	status, body = f.upload(uploadSpec{subject: "alice", kind: "zip", source: src, key: "K2", target: uuid.NewString()})
	if status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("absent target must 404 not_found, got %d (%v)", status, body)
	}
}

func TestSkillUploadTargetRequiresSingleCandidate(t *testing.T) {
	f := newSkillUploadFixture(t)
	src := buildSourceZip(t, map[string]string{
		"a/SKILL.md": srcMD("alpha", "", "a\n"),
		"b/SKILL.md": srcMD("beta", "", "b\n"),
	})
	status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: src, key: "K", target: uuid.NewString()})
	if status != http.StatusBadRequest || body["code"] != "single_candidate_required" {
		t.Fatalf("want 400 single_candidate_required, got %d (%v)", status, body)
	}
}

func TestSkillUploadMapsPartialSuccess(t *testing.T) {
	f := newSkillUploadFixture(t)
	src := buildSourceZip(t, map[string]string{
		"a/SKILL.md": srcMD("alpha", "", "a\n"),
		"a/a.txt":    "a",
		"b/SKILL.md": "---\ndescription: no name\n---\nbody\n",
	})

	status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: src, key: "K"})
	if status != http.StatusOK {
		t.Fatalf("partial success must be 200, got %d (%v)", status, body)
	}
	fails := asSlice(body["preparationFailures"])
	if len(fails) != 1 {
		t.Fatalf("want 1 preparation failure, got %d", len(fails))
	}
	pf := asMap(fails[0])
	if pf["candidateRoot"] != "b" || pf["errorCode"] == nil {
		t.Fatalf("unexpected preparation failure: %v", pf)
	}
	ings := asSlice(body["ingestions"])
	if len(ings) != 1 {
		t.Fatalf("want 1 ingestion, got %d", len(ings))
	}
	ing := asMap(ings[0])
	if ing["candidateRoot"] != "a" || ing["canonicalName"] != "alpha" || ing["state"] != "committed" {
		t.Fatalf("unexpected ingestion mapping: %v", ing)
	}
}

func TestSkillUploadFatalSourceZeroSideEffects(t *testing.T) {
	f := newSkillUploadFixture(t)
	status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: []byte("definitely not a zip archive"), key: "K"})
	if status != http.StatusBadRequest || body["code"] != "source_invalid_archive" {
		t.Fatalf("want 400 source_invalid_archive, got %d (%v)", status, body)
	}
	if skills, revisions, ingestions := f.skillRowCounts(); skills != 0 || revisions != 0 || ingestions != 0 {
		t.Fatalf("fatal source must leave zero rows, got %d/%d/%d", skills, revisions, ingestions)
	}
}

func TestSkillUploadAcceptsBodyOverGlobalJSONCeiling(t *testing.T) {
	f := newSkillUploadFixture(t)
	// Uncompressed TAR stays above the 64 KiB global JSON ceiling, proving the route-local 256 MiB
	// budget is what admits this body (the generic loop is skipped for this route).
	src := buildSourceTar(t, map[string]string{
		"a/SKILL.md": srcMD("alpha", "", "body\n"),
		"a/guide.md": strings.Repeat("x", 128*1024),
	})
	if len(src) <= 64<<10 {
		t.Fatalf("fixture must exceed 64 KiB, got %d bytes", len(src))
	}
	status, body := f.upload(uploadSpec{subject: "alice", kind: "tar", source: src, key: "K"})
	if status != http.StatusOK {
		t.Fatalf("want 200 for >64 KiB source, got %d (%v)", status, body)
	}
	if ing := asMap(asSlice(body["ingestions"])[0]); ing["state"] != "committed" {
		t.Fatalf("unexpected ingestion: %v", ing)
	}
}
