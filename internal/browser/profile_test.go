//go:build !short

package browser_test

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

const profileRoomBase = "room-profile"

// ---------------------------------------------------------------------------
// Settings dialog (theme)
// ---------------------------------------------------------------------------

// TestSettings_OpenClose verifies the settings dialog opens from the profile
// popover gear item and closes via the X button.
func TestSettings_OpenClose(t *testing.T) {
	t.Parallel()

	room := profileRoomBase + "-settings-oc"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Settings OC"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	// Open profile popover, click Settings.
	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-settings-btn").MustClick()

	page.Timeout(3 * time.Second).MustElement("#settings-dialog[open]")

	// Close via X button.
	page.MustElement("#settings-close").MustClick()
	time.Sleep(300 * time.Millisecond)
	open := page.MustEval(`() => document.getElementById('settings-dialog').open`).Bool()
	assert.False(t, open, "settings dialog should be closed")
}

// TestSettings_ThemeSwitcher verifies the three-point pill selector persists
// the chosen theme to localStorage and updates the data-theme attribute.
func TestSettings_ThemeSwitcher(t *testing.T) {
	t.Parallel()

	room := profileRoomBase + "-theme"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Theme"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	// Open settings dialog.
	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-settings-btn").MustClick()
	page.Timeout(3 * time.Second).MustElement("#settings-dialog[open]")

	// Click "Dark".
	page.MustElement(`[data-theme-value="dark"]`).MustClick()
	theme := page.MustEval(`() => document.documentElement.getAttribute('data-theme')`).Str()
	assert.Equal(t, "dark", theme)
	stored := page.MustEval(`() => localStorage.getItem('theme')`).Str()
	assert.Equal(t, "dark", stored)
	darkChecked := page.MustEval(`() => document.querySelector('[data-theme-value="dark"]').getAttribute('aria-checked')`).Str()
	assert.Equal(t, "true", darkChecked)

	// Click "Light".
	page.MustElement(`[data-theme-value="light"]`).MustClick()
	theme = page.MustEval(`() => document.documentElement.getAttribute('data-theme')`).Str()
	assert.Equal(t, "light", theme)

	// Click "Auto".
	page.MustElement(`[data-theme-value="auto"]`).MustClick()
	theme = page.MustEval(`() => document.documentElement.getAttribute('data-theme')`).Str()
	assert.Equal(t, "auto", theme)
}

// ---------------------------------------------------------------------------
// Profile dialog
// ---------------------------------------------------------------------------

// TestProfile_OpenAndLoadsContent verifies the profile dialog opens from the
// popover, lazy-loads the profile section via HTMX, and shows the user's name.
func TestProfile_OpenAndLoadsContent(t *testing.T) {
	t.Parallel()

	room := profileRoomBase + "-open"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Profile Open"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	// Open profile dialog.
	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-profile-btn").MustClick()
	page.Timeout(3 * time.Second).MustElement("#profile-dialog[open]")

	// Wait for HTMX to load the profile section.
	nameInput := page.Timeout(5 * time.Second).MustElement("#display-name")
	val := nameInput.MustProperty("value").Str()
	assert.Equal(t, "Alice", val)
}

// TestProfile_EditName verifies that changing the display name updates the
// backend and the profile popover name (via OOB swap).
func TestProfile_EditName(t *testing.T) {
	t.Parallel()

	room := profileRoomBase + "-editname"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Edit Name"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	// Open profile dialog.
	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-profile-btn").MustClick()
	page.Timeout(5 * time.Second).MustElement("#display-name")

	// Clear and type new name.
	nameInput := page.MustElement("#display-name")
	nameInput.MustSelectAllText().MustInput("Alice Renamed")

	// Submit.
	page.MustElement(`#profile-section form[hx-patch] button[type="submit"]`).MustClick()

	// Wait for HTMX to re-render profile section with success message.
	page.Timeout(5 * time.Second).MustElement(".settings-field__success")

	// Verify the input shows the new name.
	val := page.MustElement("#display-name").MustProperty("value").Str()
	assert.Equal(t, "Alice Renamed", val)

	// Verify the popover name was updated via OOB swap.
	popoverName := page.MustElement("#profile-popover-name").MustText()
	assert.Equal(t, "Alice Renamed", popoverName)

	// Verify backend state.
	u, err := ts.Redis.GetUser(context.Background(), alice.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alice Renamed", u.Name)
}

// TestProfile_DirtyDialogReloadsPage verifies that closing the profile dialog
// after making a change (name edit) triggers a full page reload so the chat
// reflects the updated profile data.
func TestProfile_DirtyDialogReloadsPage(t *testing.T) {
	t.Parallel()

	room := profileRoomBase + "-dirty-reload"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Dirty Reload"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	// Open profile dialog and change name.
	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-profile-btn").MustClick()
	nameInput := page.Timeout(5 * time.Second).MustElement("#display-name")
	nameInput.MustSelectAllText().MustInput("Alice Reloaded")
	page.MustElement(`#profile-section form[hx-patch="/user/profile"] button[type="submit"]`).MustClick()
	page.Timeout(5 * time.Second).MustElement(".settings-field__success")

	// Set a flag that will be cleared by a page reload.
	page.MustEval(`() => { window.__preReload = true; }`)

	// Close the dialog — should trigger a page reload because dirty=true.
	page.MustElement("#profile-close").MustClick()

	// Wait for close animation (~300ms) + reload + page render.
	time.Sleep(3 * time.Second)

	// After reload, the JS variable should be gone.
	flag := page.MustEval(`() => window.__preReload === true`).Bool()
	assert.False(t, flag, "page should have reloaded, clearing the JS flag")
}

// TestProfile_CleanDialogNoReload verifies that closing the profile dialog
// without making changes does NOT reload the page.
func TestProfile_CleanDialogNoReload(t *testing.T) {
	t.Parallel()

	room := profileRoomBase + "-clean-noreload"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Clean NoReload"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	// Set a JS flag that will be cleared on reload.
	page.MustEval(`() => { window.__noReloadFlag = true; }`)

	// Open and close profile dialog without changes.
	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-profile-btn").MustClick()
	page.Timeout(5 * time.Second).MustElement("#display-name")
	page.MustElement("#profile-close").MustClick()
	time.Sleep(500 * time.Millisecond) // allow close animation

	// Flag should still be set — no reload occurred.
	flag := page.MustEval(`() => window.__noReloadFlag === true`).Bool()
	assert.True(t, flag, "page should NOT have reloaded when dialog was clean")
}

// TestProfile_EditNameDuplicate verifies that submitting a name that is already
// taken by another user shows an error.
func TestProfile_EditNameDuplicate(t *testing.T) {
	t.Parallel()

	room := profileRoomBase + "-dupname"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Dup Name"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	// Open profile dialog.
	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-profile-btn").MustClick()
	page.Timeout(5 * time.Second).MustElement("#display-name")

	// Try to change name to Bob's name.
	nameInput := page.MustElement("#display-name")
	nameInput.MustSelectAllText().MustInput("Bob")
	page.MustElement(`#profile-section form[hx-patch] button[type="submit"]`).MustClick()

	// Error message should appear.
	errEl := page.Timeout(5 * time.Second).MustElement(".settings-field__error")
	assert.Contains(t, errEl.MustText(), "already taken")

	// Backend should NOT have changed.
	u, err := ts.Redis.GetUser(context.Background(), alice.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alice", u.Name)
}

// TestProfile_DeleteAccount verifies the full delete flow: checkbox + typing
// DELETE enables the button, submitting deletes the account and redirects.
func TestProfile_DeleteAccount(t *testing.T) {
	t.Parallel()

	room := profileRoomBase + "-delete"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Delete"})

	victim := model.User{ID: "user-victim", Name: "Victim", Email: "victim@example.com"}
	require.NoError(t, ts.Redis.CreateUser(context.Background(), victim))

	b := newBrowser(t)
	page := authPage(t, b, ts, victim, room)

	// Open profile dialog.
	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-profile-btn").MustClick()
	page.Timeout(5 * time.Second).MustElement("#display-name")

	// Delete button should be disabled initially.
	disabled := page.MustEval(`() => document.getElementById('delete-account-btn').disabled`).Bool()
	assert.True(t, disabled, "delete button should be disabled initially")

	// Check the checkbox — button should still be disabled (need text too).
	page.MustElement("#delete-confirm-check").MustClick()
	disabled = page.MustEval(`() => document.getElementById('delete-account-btn').disabled`).Bool()
	assert.True(t, disabled, "delete button should be disabled with only checkbox")

	// Type "DELETE" — button should become enabled.
	page.MustElement("#delete-confirm-text").MustInput("DELETE")
	time.Sleep(100 * time.Millisecond)
	disabled = page.MustEval(`() => document.getElementById('delete-account-btn').disabled`).Bool()
	assert.False(t, disabled, "delete button should be enabled after checkbox + DELETE")

	// Click delete.
	page.MustElement("#delete-account-btn").MustClick()

	// HTMX processes HX-Redirect header and navigates to /login.
	waitURLChange(t, page, "/rooms/", 10*time.Second)

	// User should be gone from Redis.
	u, err := ts.Redis.GetUser(context.Background(), victim.ID)
	require.NoError(t, err)
	assert.Nil(t, u, "user should be deleted from Redis")
}

// TestProfile_DeleteGuardPartialInput verifies the delete button stays disabled
// when only one guard condition is met.
func TestProfile_DeleteGuardPartialInput(t *testing.T) {
	t.Parallel()

	room := profileRoomBase + "-delguard"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "DelGuard"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-profile-btn").MustClick()
	page.Timeout(5 * time.Second).MustElement("#display-name")

	// Type "DELETE" without checking the box.
	page.MustElement("#delete-confirm-text").MustInput("DELETE")
	time.Sleep(100 * time.Millisecond)
	disabled := page.MustEval(`() => document.getElementById('delete-account-btn').disabled`).Bool()
	assert.True(t, disabled, "delete button should be disabled without checkbox")

	// Type wrong text with checkbox checked.
	page.MustElement("#delete-confirm-text").MustSelectAllText().MustInput("delete") // lowercase
	page.MustElement("#delete-confirm-check").MustClick()
	time.Sleep(100 * time.Millisecond)
	disabled = page.MustEval(`() => document.getElementById('delete-account-btn').disabled`).Bool()
	assert.True(t, disabled, "delete button should be disabled with wrong case")
}

// TestProfile_DisconnectProvider verifies that a connected provider can be
// disconnected when the user has multiple auth methods.
func TestProfile_DisconnectProvider(t *testing.T) {
	t.Parallel()

	room := profileRoomBase + "-disconnect"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Disconnect"})

	user := model.User{ID: "user-multi", Name: "Multi", Email: "multi@example.com"}
	require.NoError(t, ts.Redis.CreateUser(context.Background(), user))
	// Link two identities so disconnect is allowed.
	require.NoError(t, ts.Redis.LinkIdentity(context.Background(), user.ID, "github", "gh-123", "GitHub User", "https://github.com/avatar.png"))
	require.NoError(t, ts.Redis.LinkIdentity(context.Background(), user.ID, "google", "g-456", "Google User", "https://google.com/avatar.png"))

	b := newBrowser(t)
	page := authPage(t, b, ts, user, room)

	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-profile-btn").MustClick()
	page.Timeout(5 * time.Second).MustElement("#display-name")

	// Both providers should be shown as connected with Disconnect buttons.
	buttons := page.MustElements(`#profile-section form[hx-post*="/disconnect"] button`)
	assert.Equal(t, 2, len(buttons), "should show 2 disconnect buttons")

	// Disconnect GitHub.
	page.MustElement(`form[hx-post*="/github/disconnect"] button`).MustClick()
	time.Sleep(500 * time.Millisecond)

	// After re-render, GitHub should no longer have a disconnect button (it shows Connect).
	identities, err := ts.Redis.GetUserIdentities(context.Background(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, len(identities), "should have 1 identity left")
	assert.Equal(t, "google:g-456", identities[0])
}

// TestProfile_DisconnectLastProviderBlocked verifies that the user cannot
// disconnect their only auth method.
func TestProfile_DisconnectLastProviderBlocked(t *testing.T) {
	t.Parallel()

	room := profileRoomBase + "-lastprov"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "LastProv"})

	user := model.User{ID: "user-single", Name: "Single", Email: "single@example.com"}
	require.NoError(t, ts.Redis.CreateUser(context.Background(), user))
	require.NoError(t, ts.Redis.LinkIdentity(context.Background(), user.ID, "github", "gh-only", "GH Only", ""))

	b := newBrowser(t)
	page := authPage(t, b, ts, user, room)

	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-profile-btn").MustClick()
	page.Timeout(5 * time.Second).MustElement("#display-name")

	// With only one identity, the disconnect button should not be present;
	// instead "Only sign-in method" text should appear.
	hasDisconnect := page.MustEval(`() => !!document.querySelector('form[hx-post*="/disconnect"]')`).Bool()
	assert.False(t, hasDisconnect, "should not show disconnect button for last provider")

	onlyText := page.MustElement(".settings-identity__only").MustText()
	assert.Contains(t, onlyText, "Only sign-in method")

	// Identity should still be linked.
	identities, err := ts.Redis.GetUserIdentities(context.Background(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, len(identities))
}

// ---------------------------------------------------------------------------
// Authorization: cannot modify another user's profile
// ---------------------------------------------------------------------------

// TestProfile_CannotDeleteOtherUser verifies that a DELETE /user/profile
// request always deletes the authenticated user, not an arbitrary user.
// (The endpoint has no user-ID parameter — it operates on the session user.)
func TestProfile_CannotDeleteOtherUser(t *testing.T) {
	t.Parallel()

	ts := testutil.NewTestServer(t)
	room := profileRoomBase + "-authz-del"
	ts.SeedRoom(t, model.Room{ID: room, Name: "Authz Del"})

	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, room, alice.ID)
	ts.GrantAccess(t, room, bob.ID)

	// Alice tries to delete — it should delete HER account, not Bob's.
	cookie := ts.AuthCookie(t, alice)
	form := url.Values{"confirmation": {"DELETE"}}
	req, err := http.NewRequest("POST",
		ts.Server.URL+"/user/profile/delete",
		strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Alice should be gone.
	u, err := ts.Redis.GetUser(context.Background(), alice.ID)
	require.NoError(t, err)
	assert.Nil(t, u, "alice should be deleted")

	// Bob should still exist.
	u, err = ts.Redis.GetUser(context.Background(), bob.ID)
	require.NoError(t, err)
	assert.NotNil(t, u, "bob should still exist")
	assert.Equal(t, "Bob", u.Name)
}

// TestProfile_CannotRenameOtherUser verifies that a PATCH /user/profile
// request only renames the authenticated user's profile.
func TestProfile_CannotRenameOtherUser(t *testing.T) {
	t.Parallel()

	ts := testutil.NewTestServer(t)
	room := profileRoomBase + "-authz-rename"
	ts.SeedRoom(t, model.Room{ID: room, Name: "Authz Rename"})

	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, room, alice.ID)

	// Alice sends PATCH — it can only change her own name.
	cookie := ts.AuthCookie(t, alice)
	form := url.Values{"name": {"Hacked"}}
	req, err := http.NewRequest("PATCH",
		ts.Server.URL+"/user/profile",
		strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Alice's name should be changed.
	u, err := ts.Redis.GetUser(context.Background(), alice.ID)
	require.NoError(t, err)
	assert.Equal(t, "Hacked", u.Name)

	// Bob's name must not be affected.
	u, err = ts.Redis.GetUser(context.Background(), bob.ID)
	require.NoError(t, err)
	assert.Equal(t, "Bob", u.Name)
}

// TestProfile_CannotDisconnectOtherUserProvider verifies that a disconnect
// request only affects the authenticated user's identities.
func TestProfile_CannotDisconnectOtherUserProvider(t *testing.T) {
	t.Parallel()

	ts := testutil.NewTestServer(t)
	room := profileRoomBase + "-authz-disc"
	ts.SeedRoom(t, model.Room{ID: room, Name: "Authz Disc"})

	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	// Alice has github + google.
	require.NoError(t, ts.Redis.LinkIdentity(context.Background(), alice.ID, "github", "a-gh", "Alice GH", ""))
	require.NoError(t, ts.Redis.LinkIdentity(context.Background(), alice.ID, "google", "a-g", "Alice G", ""))
	// Bob has github.
	require.NoError(t, ts.Redis.LinkIdentity(context.Background(), bob.ID, "github", "b-gh", "Bob GH", ""))

	ts.GrantAccess(t, room, alice.ID)

	// Alice disconnects her github — this should NOT affect Bob's github.
	cookie := ts.AuthCookie(t, alice)
	req, err := http.NewRequest("POST",
		ts.Server.URL+"/user/identities/github/disconnect", nil)
	require.NoError(t, err)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	// Alice should have only google left.
	aliceIdents, err := ts.Redis.GetUserIdentities(context.Background(), alice.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"google:a-g"}, aliceIdents)

	// Bob's github should be untouched.
	bobIdents, err := ts.Redis.GetUserIdentities(context.Background(), bob.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"github:b-gh"}, bobIdents)
}

// TestProfile_UnauthenticatedReturns302 verifies that profile endpoints
// redirect to /login when there is no session.
func TestProfile_UnauthenticatedReturns302(t *testing.T) {
	t.Parallel()

	ts := testutil.NewTestServer(t)

	client := testutil.NoRedirectClient()
	for _, path := range []string{"/user/profile"} {
		req, err := http.NewRequest("GET", ts.Server.URL+path, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusFound, resp.StatusCode, "GET %s without auth", path)
		assert.Contains(t, resp.Header.Get("Location"), "/login")
	}

	// PATCH without auth.
	form := url.Values{"name": {"Hacker"}}
	req, err := http.NewRequest("PATCH",
		ts.Server.URL+"/user/profile",
		strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusFound, resp.StatusCode, "PATCH without auth")

	// POST delete without auth.
	form = url.Values{"confirmation": {"DELETE"}}
	req, err = http.NewRequest("POST",
		ts.Server.URL+"/user/profile/delete",
		strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err = client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusFound, resp.StatusCode, "DELETE without auth")
}
