package integration

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wanglongan587/cloud/internal/core"
)

// subscribe opens an SSE stream for the given user; it uses a dedicated client
// because streaming connections outlive the simulator client timeout.
func (f *fixture) subscribe(t *testing.T, u core.Claims, sid string, want int) *http.Response {
	t.Helper()
	gw := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}}
	svcTok, e := f.client.Credentials.Token("gateway", gw)
	must(t, e)
	u.Caller = "gateway-a"
	userTok, e := f.client.Credentials.Token("user", u)
	must(t, e)
	req, e := http.NewRequest("GET", f.cloud.URL+f.path("/spaces/"+sid+"/events"), nil)
	must(t, e)
	req.Header.Set("Authorization", "Bearer "+svcTok)
	req.Header.Set("X-Ora-User-Token", userTok)
	res, e := (&http.Client{}).Do(req)
	must(t, e)
	if res.StatusCode != want {
		res.Body.Close()
		t.Fatalf("subscribe: want %d got %d", want, res.StatusCode)
	}
	return res
}

// nextEvent waits for one SSE data line and requires it to carry the type.
func nextEvent(t *testing.T, res *http.Response, wantType string) {
	t.Helper()
	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(res.Body).ReadString('\n')
		lines <- line
	}()
	select {
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s event within 5s", wantType)
	case line := <-lines:
		if !strings.Contains(line, wantType) {
			t.Fatalf("event mismatch: want %s got %q", wantType, line)
		}
	}
}

// TestSpaceEventsAuthorizationAndCommitOrder covers scenarios 15 and 16.
func TestSpaceEventsAuthorizationAndCommitOrder(t *testing.T) {
	f := setup(t)
	gw := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}}
	space := f.createSpace("Stream", "stream", "space-stream")
	sid := space.S("id")
	bob, bobID := f.addUser(t, "bob", "Bob")
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+bobID), core.Object{"role": "member", "status": "active", "version": 0}, "", 200)
	carol, _ := f.addUser(t, "carol", "Carol") // not a member of sid

	// Scenario 15: a non-member cannot subscribe.
	refused := f.subscribe(t, carol, sid, 404)
	refused.Body.Close()

	// A member subscribes and receives committed events only.
	stream := f.subscribe(t, bob, sid, 200)
	defer stream.Body.Close()

	// Scenario 16: a rejected mutation publishes nothing. A stale version from
	// the owner conflicts after membership passes but before any update. If the
	// failed PATCH wrongly published, the first event below would be
	// space.updated instead of project.created.
	_, status, e := f.client.Call(context.Background(), "PATCH", f.path("/spaces/"+sid), "gateway", gw, &f.user, "", core.Object{"name": "Stale", "description": "", "version": 99})
	must(t, e)
	if status != 409 {
		t.Fatalf("stale patch: want 409 got %d", status)
	}

	// A committed project creation reaches the subscriber with the project id.
	_, status, e = f.client.Call(context.Background(), "POST", f.path("/spaces/"+sid+"/projects"), "gateway", gw, &bob, "stream-project", core.Object{"name": "Evented", "repositoryUrl": "https://example.invalid/repo.git", "defaultBranch": "main"})
	must(t, e)
	if status != 202 {
		t.Fatalf("create project: want 202 got %d", status)
	}
	nextEvent(t, stream, "project.created")

	// A committed space update reaches the subscriber.
	f.call("PATCH", f.path("/spaces/"+sid), core.Object{"name": "Stream Renamed", "description": "", "version": space.N("version")}, "", 200)
	nextEvent(t, stream, "space.updated")

	// The authoritative state matches the event the client was told about.
	list := f.call("GET", f.path("/spaces/"+sid+"/projects"), nil, "", 200)
	if len(list["items"].([]any)) != 1 {
		t.Fatal("project list missing evented project")
	}
}
