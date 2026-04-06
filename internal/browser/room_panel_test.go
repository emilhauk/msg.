//go:build !short

package browser_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const panelRoom = "room-panel-browser"

// TestRoomPanelToggle verifies that clicking the burger button opens the room
// settings panel and that a second click closes it again.
func TestRoomPanelToggle(t *testing.T) {
	t.Parallel()

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: panelRoom, Name: "Panel Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, panelRoom)

	// Panel should start hidden (no room-layout--panel-open class).
	isOpen := page.MustEval(`() =>
		document.getElementById('room-layout').classList.contains('room-layout--panel-open')
	`).Bool()
	assert.False(t, isOpen, "panel should be closed on initial load")

	// Click the burger toggle.
	page.MustElement("#room-panel-toggle").MustClick()

	// Wait for the HTMX request to complete by waiting for panel inner content.
	page.MustElement("#room-panel .room-panel__inner")

	isOpen = page.MustEval(`() =>
		document.getElementById('room-layout').classList.contains('room-layout--panel-open')
	`).Bool()
	assert.True(t, isOpen, "panel should be open after clicking the toggle")

	ariaExpanded := page.MustElement("#room-panel-toggle").MustAttribute("aria-expanded")
	assert.Equal(t, "true", *ariaExpanded, "toggle aria-expanded should be true when panel is open")

	// The panel should now contain the member list (alice was granted access).
	panelHTML := page.MustElement("#room-panel").MustHTML()
	assert.Contains(t, panelHTML, "Alice", "open panel should list the member name")

	// Click again to close.
	page.MustElement("#room-panel-toggle").MustClick()

	isOpen = page.MustEval(`() =>
		document.getElementById('room-layout').classList.contains('room-layout--panel-open')
	`).Bool()
	assert.False(t, isOpen, "panel should be closed after second click")

	ariaExpanded = page.MustElement("#room-panel-toggle").MustAttribute("aria-expanded")
	assert.Equal(t, "false", *ariaExpanded, "toggle aria-expanded should be false when panel is closed")
}

// TestRoomPanelMembersAndPresence verifies that the panel shows members and
// that the active indicator appears when a user is recently active.
func TestRoomPanelMembersAndPresence(t *testing.T) {
	t.Parallel()

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: panelRoom + "-presence", Name: "Presence Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))

	// Grant both users access.
	ts.GrantAccess(t, panelRoom+"-presence", alice.ID)
	ts.GrantAccess(t, panelRoom+"-presence", bob.ID)

	// Mark alice as recently active.
	require.NoError(t, ts.Redis.SetRoomLastActive(context.Background(), alice.ID, panelRoom+"-presence"))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, panelRoom+"-presence")

	// Open the panel and wait for content to load.
	page.MustElement("#room-panel-toggle").MustClick()
	page.MustElement("#room-panel .room-panel__inner")

	panelHTML := page.MustElement("#room-panel").MustHTML()
	assert.Contains(t, panelHTML, "Alice")
	assert.Contains(t, panelHTML, "Bob")
	// Alice has last_active set → active dot should appear.
	assert.Contains(t, panelHTML, "room-panel__active-dot",
		"active dot should appear for recently active member")
}

// TestAddMemberAtMention verifies that typing "@Name" in the invite input strips
// the leading "@" so the datalist can match by name, and that submitting the form
// successfully adds the member via the hidden user_id field.
func TestAddMemberAtMention(t *testing.T) {
	t.Parallel()

	// Two rooms: shared (both alice and bob) and target (alice only).
	// Bob becomes a candidate for target because they share "shared".
	sharedRoom := panelRoom + "-shared"
	targetRoom := panelRoom + "-at-mention"

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: sharedRoom, Name: "Shared Room"})
	ts.SeedRoom(t, model.Room{ID: targetRoom, Name: "Target Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, sharedRoom, alice.ID)
	ts.GrantAccess(t, sharedRoom, bob.ID)
	ts.GrantAccess(t, targetRoom, alice.ID)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, targetRoom)

	// Open the panel and wait for content.
	page.MustElement("#room-panel-toggle").MustClick()
	page.MustElement("#room-panel .room-panel__inner")

	// Type "@Bob" — the input listener should strip the leading "@".
	inviteInput := page.MustElement("#panel-invite-input")
	inviteInput.MustInput("@Bob")

	// The visible input value should be "Bob" (@ was stripped).
	val := page.MustEval(`() => document.getElementById('panel-invite-input').value`).String()
	assert.Equal(t, "Bob", val, "@ prefix should be stripped from the invite input")

	// Simulate datalist selection by setting the hidden user_id (mirrors what the
	// submit handler does when a matching option is found in the datalist).
	page.MustEval(`() => document.getElementById('panel-invite-user-id').value = '` + bob.ID + `'`)

	// Submit the form and wait for the panel to refresh.
	page.MustElement(".room-panel__add-form button[type=submit]").MustClick()
	time.Sleep(300 * time.Millisecond)

	// Bob should now appear in the members list.
	panelHTML := page.MustElement("#room-panel").MustHTML()
	assert.Contains(t, panelHTML, "Bob", "Bob should appear as a member after being added")

	// Verify server-side: bob should have access to targetRoom.
	ok, err := ts.Redis.IsRoomAccessible(context.Background(), targetRoom, bob.ID)
	require.NoError(t, err)
	assert.True(t, ok, "bob should have access to the target room after being added")
}

// TestNewRoomModal verifies that the new-room button opens the creation dialog,
// that the cancel button closes it, and that submitting the form navigates to
// the newly-created room with the correct title.
func TestNewRoomModal(t *testing.T) {
	t.Parallel()

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: panelRoom + "-newroom", Name: "New Room Test"})

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, panelRoom+"-newroom")

	// Dialog should be closed on initial load.
	open := page.MustEval(`() => document.getElementById('new-room-dialog').open`).Bool()
	assert.False(t, open, "new-room dialog should be closed initially")

	// Clicking the button should open the modal dialog.
	page.MustElement("#new-room-btn").MustClick()
	open = page.MustEval(`() => document.getElementById('new-room-dialog').open`).Bool()
	assert.True(t, open, "new-room dialog should open after clicking the button")

	// Clicking cancel should close the dialog (plays a 140 ms closing animation).
	page.MustElement("#new-room-cancel").MustClick()
	time.Sleep(250 * time.Millisecond) // wait for close animation to finish
	open = page.MustEval(`() => document.getElementById('new-room-dialog').open`).Bool()
	assert.False(t, open, "new-room dialog should close after clicking cancel")

	// Open again, fill in a name, and submit.
	page.MustElement("#new-room-btn").MustClick()
	page.MustElement(".new-room-dialog__input").MustInput("Browser Test Room")

	nav := page.WaitNavigation(proto.PageLifecycleEventNameLoad)
	page.MustElement("#new-room-dialog form [type=submit]").MustClick()
	nav()
	page.MustWaitStable()

	// Should be redirected to the new room page with the correct title.
	assert.Contains(t, page.MustInfo().URL, "/rooms/", "should be redirected to the new room")
	title := page.MustElement(".room-main__title").MustText()
	assert.Contains(t, title, "Browser Test Room", "room title should match the submitted name")
}
