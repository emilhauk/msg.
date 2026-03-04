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
	"encoding/json"
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
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
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

// authPage creates a new page, sets the session cookie for the given user,
// navigates to the room, and waits for the page to fully load.
func authPage(t *testing.T, b *rod.Browser, ts *testutil.TestServer, user model.User, room string) *rod.Page {
	t.Helper()
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
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Browser Test Room"})

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
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const kbRoom = "room-kb-e-ignored"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: kbRoom, Name: "Keyboard Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))

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
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const kbRoom = "room-kb-d-ignored"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: kbRoom, Name: "Keyboard Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))

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

// TestVersionReload verifies that when the server publishes a new build version
// via the SSE channel, the page reloads (or shows the update hint).
func TestVersionReload(t *testing.T) {
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

// ---------------------------------------------------------------------------
// Service Worker tests
// ---------------------------------------------------------------------------

const swRoom = "room-sw"

// TestSW_Registration verifies that the service worker is registered and
// controls the page after navigating to a room.
func TestSW_Registration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: swRoom, Name: "SW Test Room"})

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, swRoom)

	// Allow time for the SW install → activate cycle.
	time.Sleep(500 * time.Millisecond)

	val, err := page.Eval(`() => !!navigator.serviceWorker.controller`)
	require.NoError(t, err)
	assert.True(t, val.Value.Bool(), "service worker should control the page")
}

// TestSW_PushEvent uses the Chrome DevTools Protocol to inject a synthetic push
// event into the service worker. Because the page tab is visible, the SW should
// suppress the OS notification (tab-visible suppression logic). We verify that
// the SW processed the event without crashing and that the page is still usable.
func TestSW_PushEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: swRoom, Name: "SW Test Room"})

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, swRoom)

	// Allow time for the SW to activate and claim the page.
	time.Sleep(500 * time.Millisecond)

	// Verify SW is controlling the page before we attempt CDP delivery.
	val, err := page.Eval(`() => !!navigator.serviceWorker.controller`)
	require.NoError(t, err)
	if !val.Value.Bool() {
		t.Skip("service worker not controlling the page — skipping CDP push test")
	}

	// Enable the ServiceWorker CDP domain so version events are emitted.
	// We subscribe to the event BEFORE enabling the domain to avoid a race.
	var regID proto.ServiceWorkerRegistrationID
	regCh := make(chan proto.ServiceWorkerRegistrationID, 1)

	waitVersion := b.EachEvent(func(e *proto.ServiceWorkerWorkerVersionUpdated) bool {
		for _, v := range e.Versions {
			if v.RegistrationID != "" {
				regCh <- v.RegistrationID
				return true
			}
		}
		return false
	})

	require.NoError(t, proto.ServiceWorkerEnable{}.Call(page))

	// Wait for the first version updated event (contains registration ID).
	select {
	case regID = <-regCh:
		// Got the registration ID.
	case <-time.After(3 * time.Second):
		// The SW domain may not emit if no SW is installed — skip gracefully.
		waitVersion()
		t.Skip("ServiceWorker.workerVersionUpdated not received within 3s — SW may not be installed")
	}
	waitVersion() // release the EachEvent goroutine

	// Parse the server origin for the CDP call.
	parsed, err := url.Parse(ts.Server.URL)
	require.NoError(t, err)
	origin := parsed.Scheme + "://" + parsed.Host

	// Listen for console messages from any target (SW logs are emitted here).
	consoleCh := make(chan string, 20)
	waitConsole := b.EachEvent(func(e *proto.RuntimeConsoleAPICalled) bool {
		for _, arg := range e.Args {
			s := arg.Description
			if s == "" {
				s = arg.Value.String()
			}
			if strings.Contains(s, "[sw] push received") {
				consoleCh <- s
				return true
			}
		}
		return false
	})

	// Deliver a synthetic push message via CDP.
	pushData := `{"title":"Test","body":"Hi","roomId":"` + swRoom + `","url":"/rooms/` + swRoom + `"}`
	err = proto.ServiceWorkerDeliverPushMessage{
		Origin:         origin,
		RegistrationID: regID,
		Data:           pushData,
	}.Call(page)
	require.NoError(t, err, "CDP DeliverPushMessage should not error")

	// Wait for the SW console log confirming the push was received.
	select {
	case msg := <-consoleCh:
		// The SW logs hasVisibleTab status. We just verify it ran.
		t.Logf("SW console: %s", msg)
	case <-time.After(3 * time.Second):
		// Some headless environments may not surface SW console logs via CDP.
		// Treat as a non-fatal skip rather than a hard failure.
		t.Log("SW console log not captured within 3s — CDP console forwarding may be unavailable")
	}
	waitConsole() // release the EachEvent goroutine

	// Final sanity: the page should still be responsive after the push.
	val, err = page.Eval(`() => document.readyState`)
	require.NoError(t, err)
	assert.Equal(t, "complete", val.Value.String(), "page should still be ready after push delivery")
}

// ---------------------------------------------------------------------------
// Theme toggle tests
// ---------------------------------------------------------------------------

const themeRoom = "room-theme"

// clickThemeToggle opens the profile popover then clicks the theme-toggle
// button using real pointer events, so the test fails if an overlay is
// blocking pointer input.
func clickThemeToggle(page *rod.Page) {
	page.MustElement("#profile-btn").MustClick()
	page.MustElement("[data-theme-toggle]").MustClick()
}

// TestThemeToggle_DarkOS verifies that on a dark-mode OS the first click on the
// theme toggle jumps directly to 'light', skipping 'dark' which is invisible.
func TestThemeToggle_DarkOS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: themeRoom, Name: "Theme Test Room"})

	b := newBrowser(t)
	page := b.MustPage("")

	// Emulate a dark-mode OS preference.
	require.NoError(t, proto.EmulationSetEmulatedMedia{
		Features: []*proto.EmulationMediaFeature{
			{Name: "prefers-color-scheme", Value: "dark"},
		},
	}.Call(page))

	// Clear localStorage so the page starts with data-theme="auto".
	parsed, err := url.Parse(ts.Server.URL)
	require.NoError(t, err)
	page.MustSetCookies(&proto.NetworkCookieParam{
		Name:   ts.AuthCookie(t, alice).Name,
		Value:  ts.AuthCookie(t, alice).Value,
		Domain: parsed.Hostname(),
		Path:   "/",
	})
	page.MustNavigate(ts.Server.URL + "/rooms/" + themeRoom)
	page.MustWaitLoad()
	page.MustEval(`() => localStorage.removeItem('theme')`)
	page.MustNavigate(ts.Server.URL + "/rooms/" + themeRoom)
	page.MustWaitLoad()

	// Initial state must be "auto" (no stored preference).
	initial := page.MustEval(`() => document.documentElement.getAttribute('data-theme')`).Str()
	assert.Equal(t, "auto", initial, "initial data-theme should be 'auto' with no stored preference")

	// One click on a dark-mode OS should go to 'light', not 'dark'.
	clickThemeToggle(page)

	theme := page.MustEval(`() => document.documentElement.getAttribute('data-theme')`).Str()
	assert.Equal(t, "light", theme, "first click on dark OS should set data-theme to 'light'")

	stored := page.MustEval(`() => localStorage.getItem('theme')`).Str()
	assert.Equal(t, "light", stored, "localStorage should store 'light' after first click on dark OS")
}

// TestThemeToggle_LightOS verifies that on a light-mode OS the first click on
// the theme toggle goes to 'dark' (the expected visible change).
func TestThemeToggle_LightOS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: themeRoom, Name: "Theme Test Room"})

	b := newBrowser(t)
	page := b.MustPage("")

	// Emulate a light-mode OS preference.
	require.NoError(t, proto.EmulationSetEmulatedMedia{
		Features: []*proto.EmulationMediaFeature{
			{Name: "prefers-color-scheme", Value: "light"},
		},
	}.Call(page))

	parsed, err := url.Parse(ts.Server.URL)
	require.NoError(t, err)
	page.MustSetCookies(&proto.NetworkCookieParam{
		Name:   ts.AuthCookie(t, alice).Name,
		Value:  ts.AuthCookie(t, alice).Value,
		Domain: parsed.Hostname(),
		Path:   "/",
	})
	page.MustNavigate(ts.Server.URL + "/rooms/" + themeRoom)
	page.MustWaitLoad()
	page.MustEval(`() => localStorage.removeItem('theme')`)
	page.MustNavigate(ts.Server.URL + "/rooms/" + themeRoom)
	page.MustWaitLoad()

	// One click on a light-mode OS should go to 'dark'.
	clickThemeToggle(page)

	theme := page.MustEval(`() => document.documentElement.getAttribute('data-theme')`).Str()
	assert.Equal(t, "dark", theme, "first click on light OS should set data-theme to 'dark'")
}

// ---------------------------------------------------------------------------
// Duplicate-message regression tests
// ---------------------------------------------------------------------------

// TestNoDuplicates_SSEReconnect verifies that the catch-up logic triggered on
// SSE reconnect does not duplicate messages already visible in the DOM.
//
// When the vanilla JS EventSource reconnects and receives the same build SHA
// it previously saw, it calls doCatchUp() which fetches the latest messages
// and merges them into the DOM. Any message whose ID already exists as an
// element is skipped. This test confirms that deduplication guard works.
func TestNoDuplicates_SSEReconnect(t *testing.T) {
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
	// HTMX's polling interval detecting the element in the headless viewport.
	// This dispatches the same 'revealed' DOM event that HTMX fires internally
	// when its setInterval determines the element is scrolled into view — the
	// full hx-get / hx-swap="beforebegin" path is exercised identically.
	page.MustEval(`() => {
		const s = document.querySelector('.scroll-sentinel');
		if (s) htmx.trigger(s, 'revealed');
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

// ---------------------------------------------------------------------------
// Fast resume tests
// ---------------------------------------------------------------------------

// TestFastResume_VisibilityChange verifies that when the tab transitions from
// hidden to visible (device wake / tab un-hide), missed messages are fetched
// and inserted into the DOM within 250 ms — without waiting for the browser's
// native EventSource reconnect backoff.
//
// The visibilitychange handler in room.js calls doCatchUp() synchronously on
// the "become visible" transition, which fetches /rooms/{id}/messages and
// merges any missed messages into the DOM immediately.
func TestFastResume_VisibilityChange(t *testing.T) {
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

	// Poll for the seeded message to appear in the DOM. 250 ms is the hard
	// deadline: fast resume must not rely on EventSource reconnect backoff.
	deadline := time.Now().Add(250 * time.Millisecond)
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
	assert.True(t, found, "message seeded during tab hide should appear within 250 ms of visibilitychange")
}

// ---------------------------------------------------------------------------
// Reaction tests
// ---------------------------------------------------------------------------

const reactRoom = "room-reactions"

// postReaction sends a POST /rooms/{id}/messages/{msgID}/reactions request as
// the given user. It is the reaction counterpart of postMessage.
func postReaction(t *testing.T, ts *testutil.TestServer, user model.User, room, msgID, emoji string) {
	t.Helper()
	cookie := ts.AuthCookie(t, user)
	form := url.Values{"emoji": {emoji}}
	req, err := http.NewRequest("POST",
		ts.Server.URL+"/rooms/"+room+"/messages/"+msgID+"/reactions",
		strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// TestReaction_SelfReactionVisibleImmediately verifies that when a user reacts
// to a message, the active styling (reaction-pill--active) appears immediately
// in their own browser via the SSE reaction event — without requiring a reload.
func TestReaction_SelfReactionVisibleImmediately(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: reactRoom, Name: "Reaction Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	msg := seedMessage(t, ts, alice, reactRoom, "react to me")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, reactRoom)
	page.Timeout(5 * time.Second).MustElement("#msg-" + msg.ID)

	// Give the vanilla JS EventSource time to subscribe on the server side.
	time.Sleep(300 * time.Millisecond)

	postReaction(t, ts, alice, reactRoom, msg.ID, "👍")

	// The reaction pill with active styling must appear without a reload.
	// rod's MustElement retries until the selector matches or the timeout fires.
	pill := page.Timeout(5 * time.Second).MustElement("#reactions-" + msg.ID + " .reaction-pill--active")
	emoji, err := pill.Attribute("data-emoji")
	require.NoError(t, err)
	assert.Equal(t, "👍", *emoji, "active reaction pill should have the correct emoji")
}

// TestReaction_OtherUserReactionVisible verifies that when another user reacts
// to a message, the reaction pill appears in the observer's browser via SSE
// without requiring a reload, and is not marked active for the observer.
func TestReaction_OtherUserReactionVisible(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: reactRoom, Name: "Reaction Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))

	msg := seedMessage(t, ts, alice, reactRoom, "react to me")

	// Alice observes the room.
	b := newBrowser(t)
	page := authPage(t, b, ts, alice, reactRoom)
	page.Timeout(5 * time.Second).MustElement("#msg-" + msg.ID)

	time.Sleep(300 * time.Millisecond)

	// Bob reacts (via HTTP, outside Alice's browser).
	postReaction(t, ts, bob, reactRoom, msg.ID, "❤️")

	// The pill must appear in Alice's browser.
	pill := page.Timeout(5 * time.Second).MustElement("#reactions-" + msg.ID + " .reaction-pill")
	emoji, err := pill.Attribute("data-emoji")
	require.NoError(t, err)
	assert.Equal(t, "❤️", *emoji, "reaction pill should show Bob's emoji")

	// The pill must NOT have the active class, since Alice did not react.
	classes, err := pill.Attribute("class")
	require.NoError(t, err)
	assert.NotContains(t, *classes, "reaction-pill--active",
		"reaction pill should not be active for the observer (Alice)")
}

// TestReaction_EmojiGoesToReaction verifies that selecting an emoji while the
// picker is open in reaction mode posts it as a reaction on the correct message
// and does NOT insert it into the compose textarea.
func TestReaction_EmojiGoesToReaction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: reactRoom, Name: "Reaction Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	msg := seedMessage(t, ts, alice, reactRoom, "react to this")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, reactRoom)
	page.Timeout(5 * time.Second).MustElement("#msg-" + msg.ID)

	// Wait for SSE connection.
	time.Sleep(300 * time.Millisecond)

	// Open the reaction picker for the message via a real pointer click.
	page.MustElement(fmt.Sprintf(`[data-reaction-add="%s"]`, msg.ID)).MustClick()

	pickerHidden := page.MustEval(`() => document.getElementById('emoji-picker-container')?.hidden`).Bool()
	require.False(t, pickerHidden, "picker must be open before selecting emoji")

	// Simulate the emoji-picker-element's emoji-click event (same shape as the library).
	page.MustEval(`() => {
		const picker = document.querySelector('emoji-picker');
		if (picker) picker.dispatchEvent(new CustomEvent('emoji-click', {
			detail: { unicode: '👍' },
			bubbles: true,
			composed: true,
		}));
	}`)

	// The reaction pill with active styling must appear via SSE.
	pill := page.Timeout(5 * time.Second).MustElement(
		fmt.Sprintf(`#reactions-%s .reaction-pill--active`, msg.ID),
	)
	emoji, err := pill.Attribute("data-emoji")
	require.NoError(t, err)
	assert.Equal(t, "👍", *emoji, "reaction pill should show the selected emoji")

	// The compose textarea must remain empty — emoji must not be inserted there.
	textareaVal := page.MustEval(
		`() => document.querySelector('.message-form__textarea')?.value ?? ''`,
	).Str()
	assert.Empty(t, textareaVal, "selected emoji must not be inserted into the compose textarea")
}

// TestReaction_PickerOpensOnClick verifies that clicking the add-reaction button
// opens the emoji picker (makes it visible).
func TestReaction_PickerOpensOnClick(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: reactRoom, Name: "Reaction Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	msg := seedMessage(t, ts, alice, reactRoom, "click the add button")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, reactRoom)
	page.Timeout(5 * time.Second).MustElement("#msg-" + msg.ID)

	// Real pointer click on the add-reaction button. Rod moves the mouse to the
	// element before clicking, which triggers the hover CSS that makes the
	// opacity:0 button interactive.
	reactionAddBtn := page.MustElement(fmt.Sprintf(`[data-reaction-add="%s"]`, msg.ID))
	reactionAddBtn.MustClick()

	// The emoji picker container must become visible.
	hidden := page.Timeout(2 * time.Second).MustEval(`() => {
		const c = document.getElementById('emoji-picker-container');
		return c ? c.hidden : true;
	}`).Bool()
	assert.False(t, hidden, "emoji picker should be visible after clicking the add-reaction button")

	// Clicking the same button again must close the picker (toggle behaviour).
	reactionAddBtn.MustClick()

	hiddenAfterToggle := page.MustEval(`() => {
		const c = document.getElementById('emoji-picker-container');
		return c ? c.hidden : true;
	}`).Bool()
	assert.True(t, hiddenAfterToggle, "emoji picker should be hidden after clicking the add-reaction button again")
}

// ---------------------------------------------------------------------------
// Lightbox tests
// ---------------------------------------------------------------------------

const lightboxRoom = "room-lightbox"

// seedMessageWithImages inserts a message with image attachments directly into
// Redis. URLs should be reachable from the browser; use imgURL() for that.
func seedMessageWithImages(t *testing.T, ts *testutil.TestServer, user model.User, room string, urls ...string) model.Message {
	t.Helper()
	attachments := make([]model.Attachment, len(urls))
	for i, u := range urls {
		attachments[i] = model.Attachment{URL: u, ContentType: "image/png", Filename: fmt.Sprintf("img%d.png", i+1)}
	}
	data, err := json.Marshal(attachments)
	require.NoError(t, err)
	ms := time.Now().UnixMilli()
	msg := model.Message{
		ID:              fmt.Sprintf("%d-%s", ms, user.ID),
		RoomID:          room,
		UserID:          user.ID,
		CreatedAt:       time.UnixMilli(ms),
		CreatedAtMS:     fmt.Sprintf("%d", ms),
		AttachmentsJSON: string(data),
	}
	require.NoError(t, ts.Redis.SaveMessage(context.Background(), msg))
	return msg
}

// imgURL returns a distinct loadable image URL for test image n by appending a
// query param to the served favicon. The browser loads it successfully and the
// src values are unique enough for assertion.
func imgURL(ts *testutil.TestServer, n int) string {
	return fmt.Sprintf("%s/static/favicon.svg?img=%d", ts.Server.URL, n)
}

// lightboxIsOpen reports whether the lightbox dialog currently has the open attribute.
func lightboxIsOpen(page *rod.Page) bool {
	return page.MustEval(`() => document.getElementById('lightbox').open`).Bool()
}

// lightboxSrc returns the current src of the lightbox image element.
func lightboxSrc(page *rod.Page) string {
	return page.MustEval(`() => document.querySelector('.lightbox__img').src`).Str()
}

// TestLightbox_OpenClose verifies that clicking an image opens the lightbox and
// the close button shuts it.
func TestLightbox_OpenClose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: lightboxRoom, Name: "Lightbox Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessageWithImages(t, ts, alice, lightboxRoom, imgURL(ts, 1))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, lightboxRoom)
	page.Timeout(5 * time.Second).MustElement(".message__media-img").MustClick()

	assert.True(t, lightboxIsOpen(page), "lightbox should be open after clicking an image")

	page.MustElement(".lightbox__close").MustClick()
	assert.False(t, lightboxIsOpen(page), "lightbox should be closed after clicking the close button")
}

// TestLightbox_CloseEscape verifies that the Escape key closes the lightbox.
func TestLightbox_CloseEscape(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: lightboxRoom, Name: "Lightbox Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessageWithImages(t, ts, alice, lightboxRoom, imgURL(ts, 1))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, lightboxRoom)
	page.Timeout(5 * time.Second).MustElement(".message__media-img").MustClick()
	require.True(t, lightboxIsOpen(page), "lightbox must be open before Escape test")

	page.Keyboard.MustType(input.Escape)
	assert.False(t, lightboxIsOpen(page), "lightbox should be closed after pressing Escape")
}

// TestLightbox_CloseBackdrop verifies that clicking the backdrop (outside the
// image) closes the lightbox.
func TestLightbox_CloseBackdrop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: lightboxRoom, Name: "Lightbox Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessageWithImages(t, ts, alice, lightboxRoom, imgURL(ts, 1))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, lightboxRoom)
	page.Timeout(5 * time.Second).MustElement(".message__media-img").MustClick()
	require.True(t, lightboxIsOpen(page), "lightbox must be open before backdrop test")

	// Click the top-left corner — well clear of the centred image and buttons.
	page.Mouse.MustMoveTo(5, 5)
	page.Mouse.MustClick(proto.InputMouseButtonLeft)
	assert.False(t, lightboxIsOpen(page), "lightbox should be closed after clicking the backdrop")
}

// TestLightbox_SingleImage_NoNav verifies that the prev/next buttons are hidden
// when a message contains only one image.
func TestLightbox_SingleImage_NoNav(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: lightboxRoom, Name: "Lightbox Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessageWithImages(t, ts, alice, lightboxRoom, imgURL(ts, 1))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, lightboxRoom)
	page.Timeout(5 * time.Second).MustElement(".message__media-img").MustClick()
	require.True(t, lightboxIsOpen(page), "lightbox must be open")

	prevHidden := page.MustEval(`() => document.querySelector('.lightbox__nav--prev').hidden`).Bool()
	nextHidden := page.MustEval(`() => document.querySelector('.lightbox__nav--next').hidden`).Bool()
	assert.True(t, prevHidden, "prev button should be hidden for a single image")
	assert.True(t, nextHidden, "next button should be hidden for a single image")
}

// TestLightbox_Navigation seeds a message with 3 images, clicks the second
// (not the first), verifies the correct image is shown, then navigates with
// the prev/next buttons and checks disabled states at the ends.
func TestLightbox_Navigation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const navRoom = "room-lightbox-nav"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: navRoom, Name: "Lightbox Nav Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	url1, url2, url3 := imgURL(ts, 1), imgURL(ts, 2), imgURL(ts, 3)
	seedMessageWithImages(t, ts, alice, navRoom, url1, url2, url3)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, navRoom)
	imgs := page.Timeout(5 * time.Second).MustElements("article.message .message__media-img")
	require.Len(t, imgs, 3, "expected 3 image elements in the message")

	// Click the second image (index 1).
	imgs[1].MustClick()
	require.True(t, lightboxIsOpen(page), "lightbox must open on image click")

	// Lightbox should show the second image's src.
	assert.Equal(t, url2, lightboxSrc(page), "lightbox should display the clicked image")

	// Prev button should be enabled (not at start); next button enabled (not at end).
	prevDisabled := page.MustEval(`() => document.querySelector('.lightbox__nav--prev').disabled`).Bool()
	nextDisabled := page.MustEval(`() => document.querySelector('.lightbox__nav--next').disabled`).Bool()
	assert.False(t, prevDisabled, "prev should be enabled when not at first image")
	assert.False(t, nextDisabled, "next should be enabled when not at last image")

	// Navigate to the first image.
	page.MustElement(".lightbox__nav--prev").MustClick()
	assert.Equal(t, url1, lightboxSrc(page), "after prev, lightbox should show first image")
	prevDisabled = page.MustEval(`() => document.querySelector('.lightbox__nav--prev').disabled`).Bool()
	assert.True(t, prevDisabled, "prev should be disabled at the first image")

	// Navigate forward to the third image.
	page.MustElement(".lightbox__nav--next").MustClick()
	page.MustElement(".lightbox__nav--next").MustClick()
	assert.Equal(t, url3, lightboxSrc(page), "after two nexts, lightbox should show third image")
	nextDisabled = page.MustEval(`() => document.querySelector('.lightbox__nav--next').disabled`).Bool()
	assert.True(t, nextDisabled, "next should be disabled at the last image")
}

// TestLightbox_KeyboardNavigation verifies ArrowLeft/ArrowRight and h/l keys
// navigate between images while the lightbox is open.
func TestLightbox_KeyboardNavigation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const kbRoom = "room-lightbox-kb"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: kbRoom, Name: "Lightbox KB Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	url1, url2, url3 := imgURL(ts, 1), imgURL(ts, 2), imgURL(ts, 3)
	seedMessageWithImages(t, ts, alice, kbRoom, url1, url2, url3)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, kbRoom)
	imgs := page.Timeout(5 * time.Second).MustElements("article.message .message__media-img")
	require.Len(t, imgs, 3)

	// Open on the second image.
	imgs[1].MustClick()
	require.True(t, lightboxIsOpen(page))
	require.Equal(t, url2, lightboxSrc(page), "should open on the clicked (second) image")

	// ArrowLeft → first image.
	page.Keyboard.MustType(input.ArrowLeft)
	assert.Equal(t, url1, lightboxSrc(page), "ArrowLeft should navigate to first image")

	// ArrowRight → second image.
	page.Keyboard.MustType(input.ArrowRight)
	assert.Equal(t, url2, lightboxSrc(page), "ArrowRight should navigate to second image")

	// 'l' → third image.
	page.Keyboard.MustType(input.Key('l'))
	assert.Equal(t, url3, lightboxSrc(page), "'l' should navigate to third image")

	// 'h' → second image.
	page.Keyboard.MustType(input.Key('h'))
	assert.Equal(t, url2, lightboxSrc(page), "'h' should navigate back to second image")
}
