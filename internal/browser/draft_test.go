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

func TestDraft_PersistsOnReload(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-draft-persist"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Draft Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	ta := page.MustElement(".message-form__textarea")
	ta.MustClick()
	ta.MustInput("hello draft")

	// Verify localStorage has the draft.
	stored := page.MustEval(`() => localStorage.getItem('draft:' + window.roomID)`).Str()
	assert.Equal(t, "hello draft", stored)

	// Reload and verify textarea is restored.
	page.MustReload()
	page.MustWaitLoad()
	time.Sleep(200 * time.Millisecond)

	val := page.MustEval(`() => document.querySelector('.message-form__textarea').value`).Str()
	assert.Equal(t, "hello draft", val)
}

func TestDraft_ClearedOnSend(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-draft-send"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Draft Send Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	ta := page.MustElement(".message-form__textarea")
	ta.MustClick()
	ta.MustInput("will be sent")

	// Verify draft is saved.
	stored := page.MustEval(`() => localStorage.getItem('draft:' + window.roomID)`).Str()
	assert.Equal(t, "will be sent", stored)

	// Submit via Enter key.
	page.Keyboard.MustType(input.Enter)
	time.Sleep(500 * time.Millisecond)

	// Draft should be cleared from localStorage.
	isNull := page.MustEval(`() => localStorage.getItem('draft:' + window.roomID) === null`).Bool()
	assert.True(t, isNull, "localStorage draft key should be removed after send")

	// Textarea should be empty.
	val := page.MustEval(`() => document.querySelector('.message-form__textarea').value`).Str()
	assert.Equal(t, "", val)
}

func TestDraft_PerRoom(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const roomA = "room-draft-a"
	const roomB = "room-draft-b"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: roomA, Name: "Draft Room A"})
	ts.SeedRoom(t, model.Room{ID: roomB, Name: "Draft Room B"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	ts.GrantAccess(t, roomB, alice.ID)

	b := newBrowser(t)

	// Type a draft in room A.
	pageA := authPage(t, b, ts, alice, roomA)
	ta := pageA.MustElement(".message-form__textarea")
	ta.MustClick()
	ta.MustInput("draft for room A")

	// Navigate to room B — textarea should be empty.
	pageA.MustNavigate(ts.Server.URL + "/rooms/" + roomB)
	pageA.MustWaitLoad()
	pageA.MustElement(".message-form__textarea")
	time.Sleep(200 * time.Millisecond)

	val := pageA.MustEval(`() => document.querySelector('.message-form__textarea').value`).Str()
	assert.Equal(t, "", val, "room B should not have room A's draft")

	// Navigate back to room A — draft should be restored.
	pageA.MustNavigate(ts.Server.URL + "/rooms/" + roomA)
	pageA.MustWaitLoad()
	pageA.MustElement(".message-form__textarea")
	time.Sleep(200 * time.Millisecond)

	val = pageA.MustEval(`() => document.querySelector('.message-form__textarea').value`).Str()
	assert.Equal(t, "draft for room A", val, "room A draft should be restored")
}

func TestDraft_EmptyRemovesKey(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-draft-empty"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Draft Empty Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	ta := page.MustElement(".message-form__textarea")
	ta.MustClick()
	ta.MustInput("temporary")

	// Verify draft exists.
	stored := page.MustEval(`() => localStorage.getItem('draft:' + window.roomID)`).Str()
	assert.Equal(t, "temporary", stored)

	// Clear the textarea.
	page.MustEval(`() => {
		const ta = document.querySelector('.message-form__textarea');
		ta.value = '';
		ta.dispatchEvent(new Event('input', { bubbles: true }));
	}`)
	time.Sleep(100 * time.Millisecond)

	// localStorage key should be removed.
	isNull := page.MustEval(`() => localStorage.getItem('draft:' + window.roomID) === null`).Bool()
	assert.True(t, isNull, "localStorage draft key should be removed when textarea is emptied")
}
