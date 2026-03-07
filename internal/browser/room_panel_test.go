//go:build !short

package browser_test

import (
	"context"
	"testing"

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

// TestNewRoomForm verifies that the new-room button in the sidebar shows and
// hides the creation form, and that submitting it navigates to the new room.
func TestNewRoomForm(t *testing.T) {
	t.Parallel()

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: panelRoom + "-newroom", Name: "New Room Test"})

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, panelRoom+"-newroom")

	// Form should be hidden initially.
	formHidden := page.MustEval(`() => document.getElementById('new-room-form').hidden`).Bool()
	assert.True(t, formHidden, "new-room form should be hidden initially")

	// Click the new-room button.
	page.MustElement("#new-room-btn").MustClick()

	formHidden = page.MustEval(`() => document.getElementById('new-room-form').hidden`).Bool()
	assert.False(t, formHidden, "new-room form should appear after clicking the button")

	// Cancel hides it again.
	page.MustElement("#new-room-cancel").MustClick()
	formHidden = page.MustEval(`() => document.getElementById('new-room-form').hidden`).Bool()
	assert.True(t, formHidden, "new-room form should hide after clicking cancel")

	// Open again and submit a new room name.
	page.MustElement("#new-room-btn").MustClick()
	page.MustElement(".room-sidebar__new-input").MustInput("Browser Test Room")

	nav := page.WaitNavigation(proto.PageLifecycleEventNameLoad)
	page.MustElement(".room-sidebar__new-form").MustElement("[type=submit]").MustClick()
	nav()

	// Should now be on the new room page.
	assert.Contains(t, page.MustInfo().URL, "/rooms/", "should be redirected to the new room")
	page.MustElement(".room-main__title") // room page should have loaded
}
