//go:build !short

package browser_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod"
	"github.com/stretchr/testify/require"
)

// isHidden returns true if the element has the HTML `hidden` attribute set.
func isHidden(t *testing.T, el *rod.Element) bool {
	t.Helper()
	attr, err := el.Attribute("hidden")
	require.NoError(t, err)
	return attr != nil
}

// TestOwnerControls_InitialLoad verifies that on page load, alice's own
// messages show the edit button and bob's messages keep it hidden.
func TestOwnerControls_InitialLoad(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))

	aliceMsg := seedMessage(t, ts, alice, roomID, "hello from alice")
	time.Sleep(2 * time.Millisecond) // ensure distinct timestamps
	bobMsg := seedMessage(t, ts, bob, roomID, "hello from bob")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// alice's own message: edit button must NOT be hidden
	aliceEdit := page.Timeout(5 * time.Second).
		MustElement("#msg-" + aliceMsg.ID + " .message__edit")
	if isHidden(t, aliceEdit) {
		t.Error("alice's own message: expected edit button to be visible, but it is hidden")
	}

	// bob's message: edit button must not exist at all for alice (server omits it for non-owners)
	has, _, err := page.Has("#msg-" + bobMsg.ID + " .message__edit")
	require.NoError(t, err)
	if has {
		t.Error("bob's message: expected no edit button for alice, but one was found")
	}
}

// TestOwnerControls_SSEInsert verifies that a message posted by alice
// via the API and received via SSE shows the edit button in alice's browser.
// This is the regression test for the htmx:sseMessage vs htmx:afterSwap bug.
func TestOwnerControls_SSEInsert(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// Give the HTMX SSE connection time to subscribe on the server side.
	time.Sleep(300 * time.Millisecond)

	postMessage(t, ts, alice, roomID, "alice sse message")

	// Wait for an article to appear in the message list (SSE insert).
	article := page.Timeout(5 * time.Second).MustElement("article.message")

	// The edit button must be visible (not hidden) for alice's own message.
	editBtn := article.MustElement(".message__edit")
	if isHidden(t, editBtn) {
		t.Error("SSE-inserted own message: expected edit button to be visible, but it is hidden")
	}
}

// TestOwnerControls_SSEInsert_OtherUser verifies that a message posted by bob
// and received via SSE keeps the edit button hidden in alice's browser.
func TestOwnerControls_SSEInsert_OtherUser(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, roomID, bob.ID)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// Give the HTMX SSE connection time to subscribe on the server side.
	time.Sleep(300 * time.Millisecond)

	postMessage(t, ts, bob, roomID, "bob sse message")

	// Wait for an article to appear.
	article := page.Timeout(5 * time.Second).MustElement("article.message")

	// The edit button must remain hidden — alice cannot edit bob's message.
	editBtn := article.MustElement(".message__edit")
	if !isHidden(t, editBtn) {
		t.Error("SSE-inserted other user's message: expected edit button to be hidden, but it is visible")
	}
}
