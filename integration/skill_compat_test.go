package integration

// Skills Compatibility & Delete Repair regression, driven end-to-end over real HTTP
// (specs/decisions/cloud/skills/20260927-skill-md-metadata-contract.md as amended for
// bounded-Unicode names). It pins three real-user blocking issues:
//
//   - issue #1: a Unicode SKILL.md name (律师助手) must import and activate instead of failing
//     with skill_md_name_invalid_chars; the canonical comparison key is the NFC-normalized,
//     case-folded name — no transliteration, no ASCII slug, no pinyin;
//   - issue #3: the authored SKILL.md description must round-trip to the public read surface as
//     currentRevision.description (the browser never parses SKILL.md; the DB is the authority);
//   - issue #2: a Skill created through the real import upload must soft-delete via the public
//     DELETE route with a matching version and an Idempotency-Key, the frozen currentRevision
//     surviving in the delete response and the detail 404ing afterwards (no existence leak).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wanglongan587/cloud/internal/core"
)

// do issues one authorized JSON request against the upload fixture's live server, mirroring the
// agentFixture.do helper (same dual gateway+user credentials) for the GET/DELETE read and delete
// surfaces the upload helper does not cover.
func (f *skillUploadFixture) do(subject, method, space, suffix string, body map[string]any, key string) (int, map[string]any) {
	f.t.Helper()
	if space == "" {
		space = f.ws1
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		must(f.t, err)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, f.server.URL+"/api/v1/tenants/"+f.tid+"/spaces/"+space+suffix, rd)
	must(f.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	svc, err := f.creds.Token("gateway", core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}})
	must(f.t, err)
	req.Header.Set("Authorization", "Bearer "+svc)
	usr, err := f.creds.Token("user", core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Source: "corp", DisplayName: "Caller", Caller: "gateway-a"})
	must(f.t, err)
	req.Header.Set("X-Ora-User-Token", usr)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	must(f.t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	must(f.t, err)
	var out map[string]any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &out); err != nil {
			f.t.Fatalf("decode %q: %v", b, err)
		}
	}
	return resp.StatusCode, out
}

// TestSkillCompatChineseNameImport is the Part D combined regression for issue #1: the real
// qilinbashe-8.10.2 SKILL.md shape (name: 律师助手, description: 一个律师工作辅助 Skill) must
// import, resolve its canonical identity without transliteration, and expose both the name and
// the description through the public detail surface.
func TestSkillCompatChineseNameImport(t *testing.T) {
	f := newSkillUploadFixture(t)
	src := buildSourceZip(t, map[string]string{
		"lawyer/SKILL.md": srcMD("律师助手", "一个律师工作辅助 Skill", "body\n"),
	})

	status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: src, key: "compat-zh-1"})
	if status != http.StatusOK {
		t.Fatalf("Chinese name import must succeed, got %d (%v)", status, body)
	}
	if fails := asSlice(body["preparationFailures"]); len(fails) != 0 {
		t.Fatalf("Chinese name must not be a preparation failure, got %v", fails)
	}
	ings := asSlice(body["ingestions"])
	if len(ings) != 1 {
		t.Fatalf("want 1 ingestion, got %d", len(ings))
	}
	ing := asMap(ings[0])
	if ing["state"] != "committed" || ing["activation"] != "activated" {
		t.Fatalf("Chinese name must commit and activate, got %v", ing)
	}
	// The canonical comparison key keeps the CJK text verbatim (NFC input is already canonical);
	// no pinyin, no ASCII slug, no transliteration.
	if ing["canonicalName"] != "律师助手" {
		t.Fatalf("canonicalName must be the NFC name 律师助手, got %v", ing["canonicalName"])
	}
	skillID, _ := ing["skillId"].(string)

	// Public detail exposes the authored name and description (issue #3 chain).
	status, body = f.do("alice", http.MethodGet, f.ws1, "/skills/"+skillID, nil, "")
	if status != http.StatusOK {
		t.Fatalf("get skill: %d (%v)", status, body)
	}
	if body["displayName"] != "律师助手" {
		t.Fatalf("displayName must be the authored name 律师助手, got %v", body["displayName"])
	}
	if rev := asMap(body["currentRevision"]); rev["description"] != "一个律师工作辅助 Skill" {
		t.Fatalf("currentRevision.description must read back the authored description, got %v", rev["description"])
	}
}

// TestSkillCompatDescriptionReadsBack pins issue #3 over the full chain: SKILL.md frontmatter
// description → skillmeta → SkillRevision.package_description → public GET currentRevision.description.
func TestSkillCompatDescriptionReadsBack(t *testing.T) {
	f := newSkillUploadFixture(t)
	src := buildSourceZip(t, map[string]string{
		"a/SKILL.md": srcMD("alpha", "一个律师工作辅助 Skill", "body\n"),
	})
	status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: src, key: "compat-desc-1"})
	if status != http.StatusOK {
		t.Fatalf("upload: %d (%v)", status, body)
	}
	skillID := asMap(asSlice(body["ingestions"])[0])["skillId"].(string)

	status, body = f.do("alice", http.MethodGet, f.ws1, "/skills/"+skillID, nil, "")
	if status != http.StatusOK {
		t.Fatalf("get skill: %d (%v)", status, body)
	}
	if rev := asMap(body["currentRevision"]); rev["description"] != "一个律师工作辅助 Skill" {
		t.Fatalf("currentRevision.description must round-trip the authored description, got %q", rev["description"])
	}
}

// TestSkillCompatDeleteRoundtrip pins issue #2 on the real user path: a Skill created by the
// import upload soft-deletes via the public DELETE route with a matching version and an
// Idempotency-Key; the delete response keeps the frozen currentRevision and the detail 404s.
func TestSkillCompatDeleteRoundtrip(t *testing.T) {
	f := newSkillUploadFixture(t)
	src := buildSourceZip(t, map[string]string{
		"a/SKILL.md": srcMD("alpha", "desc", "body\n"),
	})
	status, body := f.upload(uploadSpec{subject: "alice", kind: "zip", source: src, key: "compat-del-1"})
	if status != http.StatusOK {
		t.Fatalf("upload: %d (%v)", status, body)
	}
	skillID := asMap(asSlice(body["ingestions"])[0])["skillId"].(string)

	// Read the live version, then soft-delete with a matching version + Idempotency-Key.
	_, body = f.do("alice", http.MethodGet, f.ws1, "/skills/"+skillID, nil, "")
	version := int(body["version"].(float64))
	status, body = f.do("alice", http.MethodDelete, f.ws1, "/skills/"+skillID, map[string]any{"version": version}, "compat-del-key")
	if status != http.StatusOK {
		t.Fatalf("delete must succeed, got %d (%v)", status, body)
	}
	// The delete response still exposes the frozen currentRevision (soft delete, never cleared).
	if body["id"] != skillID || asMap(body["currentRevision"])["description"] != "desc" {
		t.Fatalf("delete response must expose the frozen skill with its currentRevision, got %v", body)
	}
	// The live detail now 404s (no leak).
	if status, got := f.do("alice", http.MethodGet, f.ws1, "/skills/"+skillID, nil, ""); status != http.StatusNotFound || got["code"] != "not_found" {
		t.Fatalf("deleted skill must 404 not_found, got %d (%v)", status, got)
	}
}
