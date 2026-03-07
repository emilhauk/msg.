package handler_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRoomAccess_Forbidden verifies that every room-scoped endpoint returns 403
// when the authenticated user is not in the room's access list.
func TestRoomAccess_Forbidden(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	// alice has NO access granted
	cookie := ts.AuthCookie(t, alice)

	// Seed a message directly in Redis (bypasses access check) so DELETE/PATCH/reactions have a target.
	msg := seedMessage(t, ts, alice, "seed for access test", 0)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"POST message", "POST", "/rooms/" + testRoom + "/messages", "text=hello"},
		{"GET history", "GET", "/rooms/" + testRoom + "/messages?before=9999999999999&limit=10", ""},
		{"DELETE message", "DELETE", "/rooms/" + testRoom + "/messages/" + msg.ID, ""},
		{"PATCH message", "PATCH", "/rooms/" + testRoom + "/messages/" + msg.ID, "text=edited"},
		{"POST reaction", "POST", "/rooms/" + testRoom + "/messages/" + msg.ID + "/reactions", "emoji=👍"},
		{"GET events (SSE)", "GET", "/rooms/" + testRoom + "/events", ""},
		{"GET members", "GET", "/rooms/" + testRoom + "/members", ""},
		{"POST active", "POST", "/rooms/" + testRoom + "/active", ""},
		{"POST inactive", "POST", "/rooms/" + testRoom + "/inactive", ""},
		{"GET panel", "GET", "/rooms/" + testRoom + "/panel", ""},
		{"POST access", "POST", "/rooms/" + testRoom + "/access", "user_id=someone"},
		{"POST invites", "POST", "/rooms/" + testRoom + "/invites", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader *strings.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			} else {
				bodyReader = strings.NewReader("")
			}
			req, err := http.NewRequest(tc.method, ts.Server.URL+tc.path, bodyReader)
			require.NoError(t, err)
			if tc.body != "" && (tc.method == "POST" || tc.method == "PATCH") {
				// Check if body looks like form data or JSON
				if _, parseErr := url.ParseQuery(tc.body); parseErr == nil && !strings.HasPrefix(tc.body, "{") {
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
			}
			req.AddCookie(cookie)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			resp.Body.Close()
			assert.Equal(t, http.StatusForbidden, resp.StatusCode,
				"%s %s should return 403 when user has no room access", tc.method, tc.path)
		})
	}
}
