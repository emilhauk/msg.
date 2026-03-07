//go:build !short

package browser_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/stretchr/testify/require"
)

// enterMsgNavMode focuses the compose textarea via a real pointer click, clears
// it, then presses ArrowUp via a real keyboard event to enter message-navigation
// mode. Using real events ensures the test fails if anything (e.g. an overlay)
// is blocking pointer input.
func enterMsgNavMode(t *testing.T, page *rod.Page) {
	t.Helper()
	// Real pointer click to focus the textarea — goes through hit-testing.
	page.MustElement(".message-form__textarea").MustClick()
	// Clear value (non-interactive setup, does not test UI).
	page.MustEval(`() => document.querySelector('.message-form__textarea').value = ''`)
	// Real keyboard event to trigger the ArrowUp handler.
	page.Keyboard.MustType(input.ArrowUp)
	page.Timeout(2 * time.Second).MustElement(".message--active")
}

// pressNavKey sends a real keyboard event while in navigation mode.
// key must be a single ASCII character (e.g. "e", "d").
func pressNavKey(page *rod.Page, key string) {
	page.Keyboard.MustType(input.Key(key[0]))
}

// TestKeyboard_E_Ignored_OtherUserMessage verifies that pressing 'e' during
// message navigation does nothing when the active message is from another user.
// The SSE-broadcast path is used because that is where hidden buttons exist in
// the DOM and the guard matters most.
func TestKeyboard_E_Ignored_OtherUserMessage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const kbRoom = "room-kb-e-ignored"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: kbRoom, Name: "Keyboard Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, kbRoom, bob.ID)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, kbRoom)

	// Wait for SSE connection to be established before posting.
	time.Sleep(300 * time.Millisecond)

	// Bob posts a message; Alice receives it via SSE (buttons present but hidden).
	postMessage(t, ts, bob, kbRoom, "bob's message")
	page.Timeout(5 * time.Second).MustElement("article.message")

	// Intercept __openEdit to detect if it is called.
	page.MustEval(`() => {
		window.__openEditCalled = false;
		const orig = window.__openEdit;
		window.__openEdit = (id) => { window.__openEditCalled = true; if (orig) orig(id); };
	}`)

	enterMsgNavMode(t, page)
	pressNavKey(page, "e")
	time.Sleep(100 * time.Millisecond)

	called := page.MustEval(`() => window.__openEditCalled`).Bool()
	if called {
		t.Error("'e' key on another user's message should be ignored, but __openEdit was called")
	}
}

// TestKeyboard_D_Ignored_OtherUserMessage verifies that pressing 'd' during
// message navigation does not prompt for deletion on another user's message.
func TestKeyboard_D_Ignored_OtherUserMessage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const kbRoom = "room-kb-d-ignored"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: kbRoom, Name: "Keyboard Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, kbRoom, bob.ID)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, kbRoom)

	time.Sleep(300 * time.Millisecond)

	postMessage(t, ts, bob, kbRoom, "bob's message")
	page.Timeout(5 * time.Second).MustElement("article.message")

	// Replace window.confirm so we can detect any call without blocking.
	page.MustEval(`() => {
		window.__confirmCalled = false;
		window.confirm = () => { window.__confirmCalled = true; return false; };
	}`)

	enterMsgNavMode(t, page)
	pressNavKey(page, "d")
	time.Sleep(100 * time.Millisecond)

	called := page.MustEval(`() => window.__confirmCalled`).Bool()
	if called {
		t.Error("'d' key on another user's message should be ignored, but window.confirm was called")
	}
}

// TestKeyboard_E_Works_OwnMessage verifies that pressing 'e' opens the edit
// form when the active message belongs to the current user.
func TestKeyboard_E_Works_OwnMessage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const kbRoom = "room-kb-e-own"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: kbRoom, Name: "Keyboard Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	// Seed alice's own message on initial load (server renders buttons visible).
	seedMessage(t, ts, alice, kbRoom, "alice's own message")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, kbRoom)
	page.Timeout(5 * time.Second).MustElement("article.message")

	page.MustEval(`() => {
		window.__openEditCalled = false;
		const orig = window.__openEdit;
		window.__openEdit = (id) => { window.__openEditCalled = true; if (orig) orig(id); };
	}`)

	enterMsgNavMode(t, page)
	pressNavKey(page, "e")
	time.Sleep(100 * time.Millisecond)

	called := page.MustEval(`() => window.__openEditCalled`).Bool()
	if !called {
		t.Error("'e' key on own message should open the edit form, but __openEdit was not called")
	}
}

// TestKeyboard_D_Works_OwnMessage verifies that pressing 'd' prompts for
// deletion when the active message belongs to the current user.
func TestKeyboard_D_Works_OwnMessage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const kbRoom = "room-kb-d-own"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: kbRoom, Name: "Keyboard Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessage(t, ts, alice, kbRoom, "alice's own message")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, kbRoom)
	page.Timeout(5 * time.Second).MustElement("article.message")

	// Replace window.confirm to capture the call without blocking or deleting.
	page.MustEval(`() => {
		window.__confirmCalled = false;
		window.confirm = () => { window.__confirmCalled = true; return false; };
	}`)

	enterMsgNavMode(t, page)
	pressNavKey(page, "d")
	time.Sleep(100 * time.Millisecond)

	called := page.MustEval(`() => window.__confirmCalled`).Bool()
	if !called {
		t.Error("'d' key on own message should prompt for confirmation, but window.confirm was not called")
	}
}
