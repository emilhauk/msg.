//go:build !short

package browser_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnreadCount_HiddenTab verifies that receiving a message from another user
// while the tab is hidden increments the title prefix and swaps the favicon badge,
// and that both reset when the tab is re-activated.
func TestUnreadCount_HiddenTab(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const unreadRoom = "room-unread-hidden"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: unreadRoom, Name: "Browser Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, unreadRoom, bob.ID)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, unreadRoom)

	// Give the SSE connection time to register on the server side.
	time.Sleep(300 * time.Millisecond)

	// Open a blank tab and activate it — the room page becomes hidden.
	otherTab := b.MustPage("about:blank")
	otherTab.MustActivate()

	// Bob posts a message while the room tab is hidden.
	postMessage(t, ts, bob, unreadRoom, "hello from bob")

	// Wait for SSE delivery.
	time.Sleep(500 * time.Millisecond)

	title := page.MustEval(`() => document.title`).Str()
	assert.Equal(t, "[1] Browser Test Room — msg.", title, "title should have unread prefix")

	favicon := page.MustEval(`() => document.querySelector('link[rel="icon"]').href`).Str()
	assert.Contains(t, favicon, "favicon-badge.svg", "favicon should be the badged variant")

	// Activate the room tab — count should reset.
	page.MustActivate()
	time.Sleep(100 * time.Millisecond)

	title = page.MustEval(`() => document.title`).Str()
	assert.Equal(t, "Browser Test Room — msg.", title, "title should be restored after tab activation")

	favicon = page.MustEval(`() => document.querySelector('link[rel="icon"]').href`).Str()
	assert.NotContains(t, favicon, "favicon-badge.svg", "favicon should revert to normal after tab activation")
	assert.Contains(t, favicon, "favicon.svg", "favicon should contain favicon.svg after reset")
}

// TestUnreadCount_OwnMessageIgnored verifies that a message posted by the current
// user while the tab is hidden does not increment the unread count.
func TestUnreadCount_OwnMessageIgnored(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const unreadRoom = "room-unread-own"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: unreadRoom, Name: "Browser Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, unreadRoom)

	// Give the SSE connection time to register on the server side.
	time.Sleep(300 * time.Millisecond)

	// Open a blank tab and activate it — the room page becomes hidden.
	otherTab := b.MustPage("about:blank")
	otherTab.MustActivate()

	// Alice posts a message as herself while the room tab is hidden.
	postMessage(t, ts, alice, unreadRoom, "alice's own message")

	// Wait for SSE delivery.
	time.Sleep(500 * time.Millisecond)

	title := page.MustEval(`() => document.title`).Str()
	assert.Equal(t, "Browser Test Room — msg.", title, "own message should not increment unread count")
}
