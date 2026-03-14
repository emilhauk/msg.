//go:build !short

// Package browser_test contains end-to-end browser tests using go-rod.
// These tests launch a real headless Chromium browser and exercise the
// full client/server stack. They are excluded from the fast unit-test run
// via the !short build constraint; run them with:
//
//	go test ./internal/browser/... -v -timeout 120s
package browser_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/require"
)

const (
	roomID      = "room-browser"
	aliceUserID = "user-alice"
	bobUserID   = "user-bob"
)

var (
	alice = model.User{ID: aliceUserID, Name: "Alice", Email: "alice@example.com"}
	bob   = model.User{ID: bobUserID, Name: "Bob", Email: "bob@example.com"}
)

// newBrowser launches a Chromium browser and registers cleanup.
// Set HEADLESS=false in the environment to run with a visible browser window.
func newBrowser(t *testing.T) *rod.Browser {
	t.Helper()
	headless := os.Getenv("HEADLESS") != "false"
	l := launcher.New().Headless(headless)
	if path, exists := launcher.LookPath(); exists {
		l = l.Bin(path)
	}
	u := l.MustLaunch()
	b := rod.New().ControlURL(u).MustConnect()
	t.Cleanup(func() { b.MustClose() })
	return b
}

// authPage creates a new page, grants the user access to the room, sets the
// session cookie, navigates to the room, and waits for the page to fully load.
func authPage(t *testing.T, b *rod.Browser, ts *testutil.TestServer, user model.User, room string) *rod.Page {
	t.Helper()
	ts.GrantAccess(t, room, user.ID)

	parsed, err := url.Parse(ts.Server.URL)
	require.NoError(t, err, "authPage: parse server URL")

	cookie := ts.AuthCookie(t, user)
	page := b.MustPage("")
	page.MustSetCookies(&proto.NetworkCookieParam{
		Name:   cookie.Name,
		Value:  cookie.Value,
		Domain: parsed.Hostname(),
		Path:   "/",
	})
	page.MustNavigate(ts.Server.URL + "/rooms/" + room)
	page.MustWaitLoad()
	return page
}

// postMessage sends a POST /rooms/{id}/messages request as the given user.
func postMessage(t *testing.T, ts *testutil.TestServer, user model.User, room, text string) {
	t.Helper()
	cookie := ts.AuthCookie(t, user)
	form := url.Values{"text": {text}}
	req, err := http.NewRequest("POST",
		ts.Server.URL+"/rooms/"+room+"/messages",
		strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// patchMessage sends a PATCH /rooms/{id}/messages/{msgID} request to edit a message.
func patchMessage(t *testing.T, ts *testutil.TestServer, user model.User, room, msgID, newText string) {
	t.Helper()
	cookie := ts.AuthCookie(t, user)
	form := url.Values{"text": {newText}}
	req, err := http.NewRequest("PATCH",
		ts.Server.URL+"/rooms/"+room+"/messages/"+msgID,
		strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// waitURLChange polls page.MustInfo().URL until it no longer contains the
// given substring. This is more robust than WaitNavigation which can
// resolve from spurious CDP lifecycle events.
func waitURLChange(t *testing.T, page *rod.Page, notContains string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		u := page.MustInfo().URL
		if !strings.Contains(u, notContains) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("URL still contains %q after %v", notContains, timeout)
}

// seedMessage inserts a message directly into Redis for setup purposes.
func seedMessage(t *testing.T, ts *testutil.TestServer, user model.User, room, text string) model.Message {
	t.Helper()
	ms := time.Now().UnixMilli()
	msgID := fmt.Sprintf("%d-%s", ms, user.ID)
	msg := model.Message{
		ID:          msgID,
		RoomID:      room,
		UserID:      user.ID,
		Text:        text,
		CreatedAt:   time.UnixMilli(ms),
		CreatedAtMS: fmt.Sprintf("%d", ms),
	}
	require.NoError(t, ts.Redis.SaveMessage(context.Background(), msg))
	return msg
}
