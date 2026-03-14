//go:build !short

package browser_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod/lib/input"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const leaveRoomBase = "room-leave"

// TestLeaveRoom_PanelButton_MultiMember_Confirm verifies that when a non-last
// member clicks "Leave room" in the panel, a confirmation dialog appears and
// confirming causes the user to leave and be redirected.
func TestLeaveRoom_PanelButton_MultiMember_Confirm(t *testing.T) {
	t.Parallel()

	roomID := leaveRoomBase + "-panel-confirm"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Leave Confirm"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, roomID, bob.ID) // bob makes alice non-last member

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// Open the room panel and wait for it to load.
	page.MustElement("#room-panel-toggle").MustClick()
	page.MustElement("#room-panel .room-panel__inner")

	page.MustElement(".leave-room-btn").MustClick()

	// Confirmation dialog must appear.
	page.MustElement("#leave-room-dialog[open]")
	dialogOpen := page.MustEval(`() => document.getElementById('leave-room-dialog').open`).Bool()
	assert.True(t, dialogOpen, "leave-room dialog should open after clicking the panel button")

	// Confirm.
	page.MustElement("#leave-room-confirm").MustClick()
	waitURLChange(t, page, "/rooms/"+roomID, 5*time.Second)

	// Alice should no longer have access; bob should still have access.
	ok, err := ts.Redis.IsRoomAccessible(context.Background(), roomID, alice.ID)
	require.NoError(t, err)
	assert.False(t, ok, "alice should no longer have access after leaving")

	ok, err = ts.Redis.IsRoomAccessible(context.Background(), roomID, bob.ID)
	require.NoError(t, err)
	assert.True(t, ok, "bob should still have access")
}

// TestLeaveRoom_PanelButton_MultiMember_Cancel verifies that clicking Cancel in
// the leave-room dialog closes it without leaving the room.
func TestLeaveRoom_PanelButton_MultiMember_Cancel(t *testing.T) {
	t.Parallel()

	roomID := leaveRoomBase + "-panel-cancel"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Leave Cancel"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, roomID, bob.ID)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// Open panel and click "Leave room".
	page.MustElement("#room-panel-toggle").MustClick()
	page.MustElement("#room-panel .room-panel__inner")
	page.MustElement(".leave-room-btn").MustClick()

	page.MustElement("#leave-room-dialog[open]")

	// Cancel.
	page.MustElement("#leave-room-cancel").MustClick()
	time.Sleep(100 * time.Millisecond)

	dialogOpen := page.MustEval(`() => document.getElementById('leave-room-dialog').open`).Bool()
	assert.False(t, dialogOpen, "dialog should be closed after cancel")

	// Alice should still have access and still be on the room page.
	ok, err := ts.Redis.IsRoomAccessible(context.Background(), roomID, alice.ID)
	require.NoError(t, err)
	assert.True(t, ok, "alice should still have access after cancelling")

	url := page.MustInfo().URL
	assert.Contains(t, url, "/rooms/"+roomID, "should still be on the room page after cancel")
}

// TestLeaveRoom_Command_Confirm verifies that typing "/leave", navigating the
// command dropdown with ArrowDown, and pressing Enter executes the leave command
// (shows confirmation dialog), and confirming causes the user to leave.
func TestLeaveRoom_Command_Confirm(t *testing.T) {
	t.Parallel()

	roomID := leaveRoomBase + "-cmd-confirm"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Cmd Confirm"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, roomID, bob.ID)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// Type /leave — command dropdown should appear.
	page.MustElement(".message-form__textarea").MustInput("/leave")
	page.MustElement("#command-autocomplete:not([hidden])")

	// ArrowDown to highlight the leave command, then Enter to execute.
	page.Keyboard.MustType(input.ArrowDown)
	page.Keyboard.MustType(input.Enter)

	// triggerLeaveRoom fetches members → count > 1 → shows dialog.
	page.MustElement("#leave-room-dialog[open]")

	// Textarea should be cleared (command was consumed).
	taVal := page.MustEval(`() => document.querySelector('.message-form__textarea').value`).String()
	assert.Empty(t, taVal, "textarea should be cleared after command is executed")

	// Confirm leaving.
	page.MustElement("#leave-room-confirm").MustClick()
	waitURLChange(t, page, "/rooms/"+roomID, 5*time.Second)

	ok, err := ts.Redis.IsRoomAccessible(context.Background(), roomID, alice.ID)
	require.NoError(t, err)
	assert.False(t, ok, "alice should not have access after /leave command + confirm")
}

// TestLeaveRoom_Command_Cancel verifies that executing the /leave command (via
// ArrowDown + Enter) and then cancelling the dialog keeps the user in the room.
func TestLeaveRoom_Command_Cancel(t *testing.T) {
	t.Parallel()

	roomID := leaveRoomBase + "-cmd-cancel"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Cmd Cancel"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, roomID, bob.ID)

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// Type /leave, highlight command with ArrowDown, execute with Enter.
	page.MustElement(".message-form__textarea").MustInput("/leave")
	page.MustElement("#command-autocomplete:not([hidden])")
	page.Keyboard.MustType(input.ArrowDown)
	page.Keyboard.MustType(input.Enter)

	// Dialog should appear; textarea should be cleared.
	page.MustElement("#leave-room-dialog[open]")
	taVal := page.MustEval(`() => document.querySelector('.message-form__textarea').value`).String()
	assert.Empty(t, taVal, "textarea should be cleared after /leave command is executed")

	// Cancel the dialog.
	page.MustElement("#leave-room-cancel").MustClick()
	time.Sleep(100 * time.Millisecond)

	dialogOpen := page.MustEval(`() => document.getElementById('leave-room-dialog').open`).Bool()
	assert.False(t, dialogOpen, "dialog should be closed after cancel")

	ok, err := ts.Redis.IsRoomAccessible(context.Background(), roomID, alice.ID)
	require.NoError(t, err)
	assert.True(t, ok, "alice should still have access after cancelling /leave command")

	url := page.MustInfo().URL
	assert.Contains(t, url, "/rooms/"+roomID, "should still be on the room page")
}

// TestLeaveRoom_LastMember_NoDialog verifies that when the last member clicks
// "Leave room" in the panel, no confirmation dialog appears — the room is deleted
// immediately and the user is redirected.
func TestLeaveRoom_LastMember_NoDialog(t *testing.T) {
	t.Parallel()

	roomID := leaveRoomBase + "-last-member"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomID, Name: "Last Member"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomID)

	// Open panel and click "Leave room".
	page.MustElement("#room-panel-toggle").MustClick()
	page.MustElement("#room-panel .room-panel__inner")

	page.MustElement(".leave-room-btn").MustClick()

	// Dialog should NOT open for the last member.
	dialogOpen := page.MustEval(`() => document.getElementById('leave-room-dialog').open`).Bool()
	assert.False(t, dialogOpen, "leave dialog should NOT open for the last member")

	// Wait for navigation to complete (redirected after room deletion).
	waitURLChange(t, page, "/rooms/"+roomID, 5*time.Second)

	// Room should be gone from Redis.
	room, err := ts.Redis.GetRoom(context.Background(), roomID)
	require.NoError(t, err)
	assert.Nil(t, room, "room should be deleted from Redis when last member leaves")
}

// TestLeaveRoom_MessageIsolation verifies that leaving a room (as last member)
// deletes only that room's messages — messages in other rooms are not affected.
func TestLeaveRoom_MessageIsolation(t *testing.T) {
	t.Parallel()

	roomA := leaveRoomBase + "-isolation-a"
	roomB := leaveRoomBase + "-isolation-b"

	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomA, Name: "Room A"})
	ts.SeedRoom(t, model.Room{ID: roomB, Name: "Room B"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	ts.GrantAccess(t, roomA, alice.ID)
	ts.GrantAccess(t, roomB, alice.ID)

	// Seed one message in each room. Sleep 2ms between seeds so they get
	// distinct IDs (format: {unixMs}-{userID}; same-millisecond seeds collide).
	msgA := seedMessage(t, ts, alice, roomA, "message in room A")
	time.Sleep(2 * time.Millisecond)
	msgB := seedMessage(t, ts, alice, roomB, "message in room B")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, roomA)

	// Open panel and leave roomA (alice is the only member → no dialog).
	page.MustElement("#room-panel-toggle").MustClick()
	page.MustElement("#room-panel .room-panel__inner")

	page.MustElement(".leave-room-btn").MustClick()
	waitURLChange(t, page, "/rooms/"+roomA, 5*time.Second)

	// roomA's message should be deleted.
	deletedMsg, err := ts.Redis.GetMessage(context.Background(), msgA.ID)
	require.NoError(t, err)
	assert.Nil(t, deletedMsg, "message in deleted room A should be gone from Redis")

	// roomB's message must still exist and be unchanged.
	survivingMsg, err := ts.Redis.GetMessage(context.Background(), msgB.ID)
	require.NoError(t, err)
	require.NotNil(t, survivingMsg, "message in room B must survive room A deletion")
	assert.Equal(t, msgB.ID, survivingMsg.ID, "room B message ID should be unchanged")
	assert.Equal(t, "message in room B", survivingMsg.Text, "room B message text should be unchanged")
}
