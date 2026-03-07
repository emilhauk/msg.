package handler_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleToggle_Add(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)
	msg := seedMessage(t, ts, alice, "react to me", 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub := ts.Redis.Subscribe(ctx, testRoom)
	defer sub.Close()
	_, err := sub.Receive(ctx)
	require.NoError(t, err)

	form := url.Values{"emoji": {"👍"}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/messages/"+msg.ID+"/reactions", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	pubMsg, err := sub.ReceiveMessage(ctx)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(pubMsg.Payload, "reaction:"),
		"expected pub/sub payload to start with reaction:, got %q", pubMsg.Payload)
}

func TestHandleToggle_Off(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)
	msg := seedMessage(t, ts, alice, "toggle off", 0)

	postReaction := func() {
		form := url.Values{"emoji": {"❤️"}}
		req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/messages/"+msg.ID+"/reactions", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	}

	// First toggle: adds alice's ❤️.
	postReaction()
	reactions, err := ts.Redis.GetReactions(context.Background(), msg.ID, alice.ID)
	require.NoError(t, err)
	found := false
	for _, r := range reactions {
		if r.Emoji == "❤️" && r.ReactedByMe {
			found = true
		}
	}
	assert.True(t, found, "after first toggle alice should have ReactedByMe=true on ❤️")

	// Second toggle: removes alice's ❤️.
	postReaction()
	reactions, err = ts.Redis.GetReactions(context.Background(), msg.ID, alice.ID)
	require.NoError(t, err)
	for _, r := range reactions {
		assert.False(t, r.Emoji == "❤️" && r.ReactedByMe,
			"after second toggle alice's ❤️ should be gone")
	}
}

func TestHandleToggle_InvalidEmoji(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)
	msg := seedMessage(t, ts, alice, "no bad emoji", 0)

	tests := []struct {
		name  string
		emoji string
	}{
		{"empty emoji", ""},
		{"too long emoji", "abcdefghi"}, // 9 chars > 8 rune limit
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"emoji": {tc.emoji}}
			req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/messages/"+msg.ID+"/reactions", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(cookie)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}
