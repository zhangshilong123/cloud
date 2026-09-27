package router

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/simulator"
)

// uploadTestContext builds a gin context whose request carries dual verified credentials for the
// given caller identity plus a multipart body assembled from text fields and one file part. The
// store is nil: every assertion in this file returns before the handler reaches identity/storage,
// so no database is involved.
func uploadTestContext(t *testing.T, creds *simulator.Credentials, subject, source, key string, fields map[string][]string, files map[string][]byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, values := range fields {
		for _, v := range values {
			if err := mw.WriteField(name, v); err != nil {
				t.Fatalf("write field %s: %v", name, err)
			}
		}
	}
	for name, data := range files {
		fw, err := mw.CreateFormFile(name, "source.bin")
		if err != nil {
			t.Fatalf("create file %s: %v", name, err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatalf("write file %s: %v", name, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants/tid/spaces/spaceId/skills/imports", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	svc, err := creds.Token("gateway", core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}})
	if err != nil {
		t.Fatalf("service token: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+svc)
	user, err := creds.Token("user", core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Source: source, DisplayName: "Caller", Caller: "gateway-a"})
	if err != nil {
		t.Fatalf("user token: %v", err)
	}
	req.Header.Set("X-Ora-User-Token", user)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	return c, rec
}

func uploadCredentials(t *testing.T) (*simulator.Credentials, *core.Authenticator) {
	t.Helper()
	creds, err := simulator.NewCredentials()
	if err != nil {
		t.Fatalf("credentials: %v", err)
	}
	auth, err := core.NewAuthenticator("ora-cloud", creds.Trust)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	return creds, auth
}

func uploadCode(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode response %q: %v", rec.Body.String(), err)
		}
	}
	code, _ := out["code"].(string)
	return code, out
}

// TestUploadRejectsMissingIdempotencyKey pins D6: Idempotency-Key is required before any body is
// buffered, so a malformed request fails fast.
func TestUploadRejectsMissingIdempotencyKey(t *testing.T) {
	creds, auth := uploadCredentials(t)
	c, rec := uploadTestContext(t, creds, "alice", "corp", "", map[string][]string{"source_kind": {"zip"}}, map[string][]byte{"source": []byte("x")})
	uploadSkillSource(nil, auth, zap.NewNop(), c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
	if code, _ := uploadCode(t, rec); code != "idempotency_key_required" {
		t.Fatalf("want idempotency_key_required, got %q", code)
	}
}

func TestUploadRejectsEmptyIdempotencyKey(t *testing.T) {
	creds, auth := uploadCredentials(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants/tid/spaces/spaceId/skills/imports", strings.NewReader("x"))
	svc, _ := creds.Token("gateway", core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}})
	user, _ := creds.Token("user", core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "alice"}, Source: "corp", Caller: "gateway-a"})
	req.Header.Set("Authorization", "Bearer "+svc)
	req.Header.Set("X-Ora-User-Token", user)
	req.Header.Set("Idempotency-Key", "")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	uploadSkillSource(nil, auth, zap.NewNop(), c)
	if code, _ := uploadCode(t, rec); code != "invalid_idempotency_key" {
		t.Fatalf("want invalid_idempotency_key, got %q", code)
	}
}

// TestUploadRejectsOversizedBodyViaContentLength pins D14: the cheap known-length check rejects a
// body larger than the 256 MiB budget before any buffering (413 upload_too_large), never reading
// the oversized body into memory.
func TestUploadRejectsOversizedBodyViaContentLength(t *testing.T) {
	creds, auth := uploadCredentials(t)
	c, rec := uploadTestContext(t, creds, "alice", "corp", "key", map[string][]string{"source_kind": {"zip"}}, map[string][]byte{"source": []byte("x")})
	c.Request.ContentLength = skillsUploadBudget + 1
	uploadSkillSource(nil, auth, zap.NewNop(), c)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", rec.Code)
	}
	if code, _ := uploadCode(t, rec); code != "upload_too_large" {
		t.Fatalf("want upload_too_large, got %q", code)
	}
}

// TestUploadRejectsMissingSourceKind pins D5: source_kind is required and its absence is a
// missing_field fault, not a silent default.
func TestUploadRejectsMissingSourceKind(t *testing.T) {
	creds, auth := uploadCredentials(t)
	c, rec := uploadTestContext(t, creds, "alice", "corp", "key", nil, map[string][]byte{"source": []byte("x")})
	uploadSkillSource(nil, auth, zap.NewNop(), c)
	if code, out := uploadCode(t, rec); code != "missing_field" || out["params"].(map[string]any)["field"] != "source_kind" {
		t.Fatalf("want missing_field/source_kind, got %q %v", code, out["params"])
	}
}

func TestUploadRejectsUnknownSourceKind(t *testing.T) {
	creds, auth := uploadCredentials(t)
	c, rec := uploadTestContext(t, creds, "alice", "corp", "key", map[string][]string{"source_kind": {"rar"}}, map[string][]byte{"source": []byte("x")})
	uploadSkillSource(nil, auth, zap.NewNop(), c)
	if code, _ := uploadCode(t, rec); code != "source_unsupported_source_kind" {
		t.Fatalf("want source_unsupported_source_kind, got %q", code)
	}
}

func TestUploadRejectsMissingSource(t *testing.T) {
	creds, auth := uploadCredentials(t)
	c, rec := uploadTestContext(t, creds, "alice", "corp", "key", map[string][]string{"source_kind": {"zip"}}, nil)
	uploadSkillSource(nil, auth, zap.NewNop(), c)
	if code, out := uploadCode(t, rec); code != "missing_field" || out["params"].(map[string]any)["field"] != "source" {
		t.Fatalf("want missing_field/source, got %q %v", code, out["params"])
	}
}

// TestUploadRejectsUnknownAndForbiddenFields pins D5: any field outside the allowlist — including
// the forbidden server-owned canonical_name/content_digest/package_digest/object_locator/revision_id
// — is an unknown_field fault.
func TestUploadRejectsUnknownAndForbiddenFields(t *testing.T) {
	for _, field := range []string{"canonical_name", "content_digest", "package_digest", "object_locator", "revision_id", "nonsense"} {
		creds, auth := uploadCredentials(t)
		c, rec := uploadTestContext(t, creds, "alice", "corp", "key", map[string][]string{"source_kind": {"zip"}, field: {"x"}}, map[string][]byte{"source": []byte("x")})
		uploadSkillSource(nil, auth, zap.NewNop(), c)
		if code, out := uploadCode(t, rec); code != "unknown_field" || out["params"].(map[string]any)["field"] != field {
			t.Fatalf("field %s: want unknown_field/%s, got %q %v", field, field, code, out["params"])
		}
	}
}

func TestUploadRejectsDuplicateField(t *testing.T) {
	creds, auth := uploadCredentials(t)
	c, rec := uploadTestContext(t, creds, "alice", "corp", "key", map[string][]string{"source_kind": {"zip", "tar"}}, map[string][]byte{"source": []byte("x")})
	uploadSkillSource(nil, auth, zap.NewNop(), c)
	if code, _ := uploadCode(t, rec); code != "duplicate_field" {
		t.Fatalf("want duplicate_field, got %q", code)
	}
}

func TestUploadRejectsOversizedDisplayNameAndSummary(t *testing.T) {
	creds, auth := uploadCredentials(t)
	c, rec := uploadTestContext(t, creds, "alice", "corp", "key", map[string][]string{"source_kind": {"zip"}, "display_name": {strings.Repeat("a", 201)}}, map[string][]byte{"source": []byte("x")})
	uploadSkillSource(nil, auth, zap.NewNop(), c)
	if code, _ := uploadCode(t, rec); code != "invalid_field_type" {
		t.Fatalf("want invalid_field_type for display_name, got %q", code)
	}

	c, rec = uploadTestContext(t, creds, "alice", "corp", "key", map[string][]string{"source_kind": {"zip"}, "summary": {strings.Repeat("a", 4097)}}, map[string][]byte{"source": []byte("x")})
	uploadSkillSource(nil, auth, zap.NewNop(), c)
	if code, _ := uploadCode(t, rec); code != "invalid_field_type" {
		t.Fatalf("want invalid_field_type for summary, got %q", code)
	}
}

// TestUploadRejectsInvalidArchive pins D7: a structural source failure (corrupt archive → zero
// candidates) is a 400 source_* fault, never a 200 envelope.
func TestUploadRejectsInvalidArchive(t *testing.T) {
	creds, auth := uploadCredentials(t)
	c, rec := uploadTestContext(t, creds, "alice", "corp", "key", map[string][]string{"source_kind": {"zip"}}, map[string][]byte{"source": []byte("definitely not a zip archive")})
	uploadSkillSource(nil, auth, zap.NewNop(), c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
	if code, _ := uploadCode(t, rec); code != "source_invalid_archive" {
		t.Fatalf("want source_invalid_archive, got %q", code)
	}
}
