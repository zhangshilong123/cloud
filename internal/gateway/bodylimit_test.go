package gateway

import (
	"net/http"
	"testing"
)

// TestRequestBodyLimitPinsTheRouteLocalExemption locks the Gateway body-limit contract
// from specs/decisions/cloud/skills/20260927-public-upload-api-contract.md (D14): only the
// Skills source upload route may carry more than the 64 KiB global ceiling, and the global
// ceiling itself is never widened.
func TestRequestBodyLimitPinsTheRouteLocalExemption(t *testing.T) {
	upload := "/api/v1/tenants/tid/spaces/spaceId/skills/imports"
	if got := requestBodyLimit(http.MethodPost, upload); got != skillsUploadBudget {
		t.Fatalf("skills upload POST must use its own budget: got %d want %d", got, skillsUploadBudget)
	}
	// A GET to the same shape is not an upload and keeps the global ceiling.
	if got := requestBodyLimit(http.MethodGet, upload); got != maxBodyBytes {
		t.Fatalf("skills upload GET must keep the global ceiling: got %d want %d", got, maxBodyBytes)
	}
	// Every other route — including a space member POST that shares the same path prefix —
	// keeps the 64 KiB global ceiling.
	for _, path := range []string{
		"/api/v1/tenants/tid/spaces/spaceId/members",
		"/api/v1/tenants/tid/spaces/spaceId/projects",
		"/api/v1/tenants/tid/spaces",
		"/api/v1/tenants/tid/projects",
		"/api/v1/me",
	} {
		if got := requestBodyLimit(http.MethodPost, path); got != maxBodyBytes {
			t.Fatalf("POST %s must keep the 64 KiB global ceiling: got %d", path, got)
		}
	}
	// The global ceiling is unchanged at 64 KiB.
	if maxBodyBytes != 64<<10 {
		t.Fatalf("global maxBodyBytes must stay 64 KiB, got %d", maxBodyBytes)
	}
}

// TestIsSkillsUploadPathPinsTheSegmentShape guards the route-aware matcher against drift:
// it must match only the exact 8-segment skills/imports shape, never a shorter/longer path
// or a lookalike, so the 256 MiB exemption can never be widened to a sibling route.
func TestIsSkillsUploadPathPinsTheSegmentShape(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/api/v1/tenants/tid/spaces/spaceId/skills/imports", true},
		{"/api/v1/tenants/tid/spaces/spaceId/skills/imports/", false},
		{"/api/v1/tenants/tid/spaces/spaceId/skills", false},
		{"/api/v1/tenants/tid/spaces/spaceId/skills/imports/extra", false},
		{"/api/v1/tenants/tid/spaces/spaceId/projects", false},
		{"/api/v1/tenants/tid/spaces/spaceId/skills/import", false},
		{"/internal/v1/tenants/tid/spaces/spaceId/skills/imports", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isSkillsUploadPath(tc.path); got != tc.want {
			t.Errorf("isSkillsUploadPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
