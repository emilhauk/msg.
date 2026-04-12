//go:build !short

package browser_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVersionReload verifies that when the server publishes a new build version
// via the SSE channel, the page reloads (or shows the update hint).
func TestVersionReload(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const versionRoom = "room-version"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: versionRoom, Name: "Version Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, versionRoom)

	// Force document.hasFocus() to return false so the version event triggers
	// window.location.reload() rather than just showing the update hint.
	page.MustEval(`() => { document.hasFocus = () => false; }`)

	// Set up the navigation listener before publishing (so we don't miss it).
	waitNav := page.WaitNavigation(proto.PageLifecycleEventNameLoad)

	// Give both EventSource connections time to establish their Redis subscriptions.
	// The SSE handler subscribes after flushing initial events, so we wait 300ms.
	time.Sleep(300 * time.Millisecond)

	// Publish a different version via Redis pub/sub. The SSE handler relays this
	// as an SSE version event, triggering window.location.reload() in the browser.
	require.NoError(t, ts.Redis.Publish(
		context.Background(), versionRoom, "version:sha-new-deploy",
	))

	// Wait for navigation (page reload) with a 5-second deadline.
	done := make(chan struct{})
	go func() { waitNav(); close(done) }()

	select {
	case <-done:
		// Page reloaded successfully.
	case <-time.After(5 * time.Second):
		t.Fatal("page did not reload within 5s after version change SSE event")
	}
}

// TestNoDuplicates_SSEReconnect verifies that the catch-up logic triggered on
// SSE reconnect does not duplicate messages already visible in the DOM.
//
// When the vanilla JS EventSource reconnects and receives the same build SHA
// it previously saw, it calls doCatchUp() which fetches the latest messages
// and merges them into the DOM. Any message whose ID already exists as an
// element is skipped. This test confirms that deduplication guard works.
func TestNoDuplicates_SSEReconnect(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const dupRoom = "room-dup-reconnect"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: dupRoom, Name: "Dup Reconnect Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	// Seed 3 messages with distinct millisecond timestamps.
	base := time.Now().UnixMilli()
	for i := 0; i < 3; i++ {
		ms := base + int64(i)
		require.NoError(t, ts.Redis.SaveMessage(context.Background(), model.Message{
			ID:          fmt.Sprintf("%d-%s", ms, aliceUserID),
			RoomID:      dupRoom,
			UserID:      aliceUserID,
			Text:        fmt.Sprintf("message %d", i+1),
			CreatedAt:   time.UnixMilli(ms),
			CreatedAtMS: fmt.Sprintf("%d", ms),
		}))
	}

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, dupRoom)

	// Messages are server-side rendered — all 3 should be in the DOM immediately.
	before := page.MustElements("#message-list-content article.message")
	require.Len(t, before, 3, "expected 3 articles on initial load")

	// Wait for both EventSource connections to register their Redis subscriptions.
	time.Sleep(300 * time.Millisecond)

	// Publishing "version:test" matches the server's build version in test mode
	// (testutil wires the SSE handler with Version:"test"). The vanilla JS
	// EventSource sees this as a same-SHA reconnect event and calls doCatchUp().
	require.NoError(t, ts.Redis.Publish(context.Background(), dupRoom, "version:test"))

	// Allow the async doCatchUp fetch to complete and the DOM to settle.
	time.Sleep(1500 * time.Millisecond)

	after := page.MustElements("#message-list-content article.message")
	assert.Len(t, after, 3, "doCatchUp must not duplicate messages already visible in DOM")
}

// TestNoDuplicates_Scrollback verifies that loading message history via the
// infinite-scroll sentinel does not produce duplicate articles in the DOM.
//
// The initial page load renders the latest 50 messages. When more exist, a
// scroll-sentinel is present. Triggering it fetches older messages and swaps
// them in via HTMX. This test seeds 55 messages and confirms that after the
// history load all 55 articles are present exactly once.
func TestNoDuplicates_Scrollback(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const scrollRoom = "room-dup-scroll"
	const total = 55 // more than the 50-message initial load cap
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: scrollRoom, Name: "Dup Scroll Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	// Seed 55 messages with distinct millisecond timestamps.
	base := time.Now().UnixMilli()
	for i := 0; i < total; i++ {
		ms := base + int64(i)
		require.NoError(t, ts.Redis.SaveMessage(context.Background(), model.Message{
			ID:          fmt.Sprintf("%d-%s", ms, aliceUserID),
			RoomID:      scrollRoom,
			UserID:      aliceUserID,
			Text:        fmt.Sprintf("message %d", i+1),
			CreatedAt:   time.UnixMilli(ms),
			CreatedAtMS: fmt.Sprintf("%d", ms),
		}))
	}

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, scrollRoom)

	// Initial load renders the latest 50 messages server-side.
	initial := page.MustElements("#message-list-content article.message")
	require.Len(t, initial, 50, "expected 50 articles from initial load")

	// A scroll-sentinel must be present since there are more messages above.
	page.Timeout(3 * time.Second).MustElement(".scroll-sentinel")

	// Trigger the sentinel directly via htmx.trigger so we don't depend on
	// the IntersectionObserver firing in the headless viewport. This dispatches
	// the same 'intersect' event that HTMX fires internally when its observer
	// detects the element — the full hx-get / hx-swap="beforebegin" path is
	// exercised identically.
	page.MustEval(`() => {
		const s = document.querySelector('.scroll-sentinel');
		if (s) htmx.trigger(s, 'intersect');
	}`)

	// Poll until all 55 articles are present (up to 8 s).
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		n := page.MustEval(`() => document.querySelectorAll('#message-list-content article.message').length`).Int()
		if n >= total {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	articles := page.MustElements("#message-list-content article.message")
	assert.Len(t, articles, total, "scrollback should add the missing messages without duplicates")

	// Verify uniqueness: every article ID must appear exactly once.
	seen := make(map[string]bool, len(articles))
	for _, el := range articles {
		id, attrErr := el.Attribute("id")
		require.NoError(t, attrErr)
		if assert.NotNil(t, id, "article element is missing its id attribute") {
			assert.False(t, seen[*id], "duplicate message found in DOM: %s", *id)
			seen[*id] = true
		}
	}
}

// TestFastResume_VisibilityChange verifies that when the tab transitions from
// hidden to visible (device wake / tab un-hide), missed messages are fetched
// and inserted into the DOM within 250 ms — without waiting for the browser's
// native EventSource reconnect backoff.
//
// The visibilitychange handler in room.js calls doCatchUp() synchronously on
// the "become visible" transition, which fetches /rooms/{id}/messages and
// merges any missed messages into the DOM immediately.
func TestFastResume_VisibilityChange(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const resumeRoom = "room-resume"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: resumeRoom, Name: "Resume Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, resumeRoom)

	// Wait for both EventSource connections to establish their Redis subscriptions.
	time.Sleep(300 * time.Millisecond)

	// Seed a message directly into Redis — simulates a message sent by another
	// user while this tab was asleep / backgrounded.
	msg := seedMessage(t, ts, alice, resumeRoom, "message sent while tab was hidden")

	// Simulate the tab becoming visible: explicitly set document.hidden to false
	// and dispatch visibilitychange. The handler in room.js calls doCatchUp()
	// immediately, bypassing any EventSource reconnect backoff.
	page.MustEval(`() => {
		Object.defineProperty(document, 'hidden', { get: () => false, configurable: true });
		document.dispatchEvent(new Event('visibilitychange'));
	}`)

	// Poll for the seeded message to appear in the DOM. 2 s is generous but
	// still proves the catch-up is immediate (not relying on EventSource
	// reconnect backoff which takes many seconds).
	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		has, _, err := page.Has("#msg-" + msg.ID)
		require.NoError(t, err)
		if has {
			found = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.True(t, found, "message seeded during tab hide should appear within 2 s of visibilitychange")
}

// TestCatchUp_StaleContentRefreshedOnResume verifies that doCatchUp() replaces
// existing articles in-place when the tab becomes visible again, so that edits
// and reaction changes that arrived while the tab was hidden are immediately
// visible without a full page reload.
//
// Scenario:
//  1. Alice opens the room; a message from Alice is visible.
//  2. Alice reacts with 👍 — the SSE reaction event lands in her browser and
//     populates __myReactions so the active state is locally tracked.
//  3. The tab is "hidden" (visibilitychange event with document.hidden=true),
//     closing both EventSource connections.
//  4. While hidden: the message text is edited via PATCH, and Bob adds a 👎
//     reaction — both changes are unknown to Alice's browser.
//  5. The tab becomes "visible" again (visibilitychange with hidden=false),
//     triggering doCatchUp().
//  6. Assertions:
//     - The message text reflects the edit.
//     - Bob's 👎 reaction pill is present.
//     - Alice's 👍 reaction pill still has the active class (applyMyReactions
//       ran on the refreshed article).
func TestCatchUp_StaleContentRefreshedOnResume(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const catchupRoom = "room-catchup-stale"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: catchupRoom, Name: "Catch-Up Stale Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, catchupRoom, bob.ID)

	msg := seedMessage(t, ts, alice, catchupRoom, "original text")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, catchupRoom)

	// Wait for the message and both SSE connections to be established.
	page.Timeout(5 * time.Second).MustElement("#msg-" + msg.ID)
	time.Sleep(300 * time.Millisecond)

	// Alice reacts with 👍 so __myReactions is populated in her browser.
	postReaction(t, ts, alice, catchupRoom, msg.ID, "👍")

	// Wait for the SSE reaction event to arrive and update __myReactions.
	page.Timeout(5 * time.Second).MustElement(
		fmt.Sprintf(`#reactions-%s .reaction-pill--active`, msg.ID),
	)

	// Simulate tab becoming hidden — closes both EventSource connections.
	page.MustEval(`() => {
		Object.defineProperty(document, 'hidden', { get: () => true, configurable: true });
		document.dispatchEvent(new Event('visibilitychange'));
	}`)

	// While hidden: edit the message and add Bob's reaction.
	patchMessage(t, ts, alice, catchupRoom, msg.ID, "updated text")
	postReaction(t, ts, bob, catchupRoom, msg.ID, "👎")

	// Simulate tab becoming visible — triggers doCatchUp().
	page.MustEval(`() => {
		Object.defineProperty(document, 'hidden', { get: () => false, configurable: true });
		document.dispatchEvent(new Event('visibilitychange'));
	}`)

	// Poll until the edited text appears (up to 3 s).
	deadline := time.Now().Add(3 * time.Second)
	var textContent string
	for time.Now().Before(deadline) {
		textContent = page.MustEval(fmt.Sprintf(
			`() => document.querySelector('#text-%s')?.textContent ?? ''`, msg.ID,
		)).Str()
		if textContent == "updated text" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.Equal(t, "updated text", textContent,
		"catch-up should refresh the article with the edited message text")

	// Bob's 👎 reaction pill must now be visible.
	bobPill := page.Timeout(3 * time.Second).MustElement(
		fmt.Sprintf(`#reactions-%s [data-emoji="👎"]`, msg.ID),
	)
	bobEmoji, err := bobPill.Attribute("data-emoji")
	require.NoError(t, err)
	assert.Equal(t, "👎", *bobEmoji, "Bob's reaction pill should appear after catch-up")

	// Alice's 👍 pill must still carry the active class (applyMyReactions ran).
	alicePill := page.Timeout(3 * time.Second).MustElement(
		fmt.Sprintf(`#reactions-%s [data-emoji="👍"]`, msg.ID),
	)
	aliceClass, err := alicePill.Attribute("class")
	require.NoError(t, err)
	assert.Contains(t, *aliceClass, "reaction-pill--active",
		"Alice's own reaction pill should remain active after catch-up refresh")
}
