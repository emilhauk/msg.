//go:build !short

package browser_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod/lib/input"
	"github.com/stretchr/testify/require"
)

// TestInputMode_PhysicalKeyboard_EnterSends verifies that on a device with a
// fine pointer (physical keyboard), pressing Enter in the message textarea
// submits the form instead of inserting a newline.
func TestInputMode_PhysicalKeyboard_EnterSends(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-input-phys-send"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Input Mode Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	// Wait for SSE to establish.
	time.Sleep(300 * time.Millisecond)

	// Default Chromium emulates pointer: fine — so Enter should send.
	// Ensure __isVirtualKeyboard returns false.
	isVirtual := page.MustEval(`() => window.__isVirtualKeyboard()`).Bool()
	require.False(t, isVirtual, "desktop browser should report physical keyboard")

	// Type a message and press Enter.
	ta := page.MustElement(".message-form__textarea")
	ta.MustClick()
	ta.MustInput("hello from physical keyboard")
	page.Keyboard.MustType(input.Enter)

	// The message should appear via SSE (form was submitted).
	page.Timeout(5 * time.Second).MustElement("article.message")
	text := page.MustElement("article.message .message__text").MustText()
	require.Contains(t, text, "hello from physical keyboard")

	// Textarea should be cleared after submit.
	val := page.MustEval(`() => document.querySelector('.message-form__textarea').value`).String()
	require.Empty(t, val, "textarea should be cleared after send")
}

// TestInputMode_VirtualKeyboard_EnterNewline verifies that when the input mode
// is virtual (touch), pressing Enter inserts a newline instead of submitting.
func TestInputMode_VirtualKeyboard_EnterNewline(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-input-virt-newline"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Input Mode Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	// Wait for SSE to establish.
	time.Sleep(300 * time.Millisecond)

	// Force virtual keyboard mode by overriding the flag.
	page.MustEval(`() => { window.__isVirtualKeyboard = () => true; }`)

	// Type text and press Enter.
	ta := page.MustElement(".message-form__textarea")
	ta.MustClick()
	ta.MustInput("line one")
	page.Keyboard.MustType(input.Enter)

	// Give a moment for any submission to happen (it shouldn't).
	time.Sleep(200 * time.Millisecond)

	// No message article should have been created.
	articles := page.MustElements("article.message")
	require.Empty(t, articles, "Enter on virtual keyboard should not send the message")

	// The textarea should still contain the text (Enter produced a newline).
	val := page.MustEval(`() => document.querySelector('.message-form__textarea').value`).String()
	require.Contains(t, val, "line one", "textarea should still contain the typed text")
}

// TestInputMode_VirtualKeyboard_EditEnterNewline verifies that when in virtual
// keyboard mode, pressing Enter in an edit textarea inserts a newline instead
// of submitting the edit.
func TestInputMode_VirtualKeyboard_EditEnterNewline(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-input-virt-edit"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Input Mode Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	msg := seedMessage(t, ts, alice, room, "original text")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)
	page.Timeout(5 * time.Second).MustElement("article.message")

	// Force virtual keyboard mode.
	page.MustEval(`() => { window.__isVirtualKeyboard = () => true; }`)

	// Open the edit form.
	page.MustEval(`(id) => window.__openEdit(id)`, msg.ID)
	editTA := page.Timeout(2 * time.Second).MustElement("#edit-form-" + msg.ID + " textarea")

	// Press Enter in the edit textarea.
	editTA.MustClick()
	page.Keyboard.MustType(input.Enter)

	// Give a moment for any submission to happen (it shouldn't).
	time.Sleep(200 * time.Millisecond)

	// The edit form should still be visible (not submitted and closed).
	formHidden := page.MustEval(`(id) => document.getElementById('edit-form-' + id).hidden`, msg.ID).Bool()
	require.False(t, formHidden, "edit form should remain open (Enter should not submit on virtual keyboard)")
}

// TestInputMode_PhysicalKeyboard_EditEnterSubmits verifies that on a physical
// keyboard, pressing Enter in an edit textarea submits the edit.
func TestInputMode_PhysicalKeyboard_EditEnterSubmits(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-input-phys-edit"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Input Mode Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	msg := seedMessage(t, ts, alice, room, "original text")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)
	page.Timeout(5 * time.Second).MustElement("article.message")

	// Wait for SSE.
	time.Sleep(300 * time.Millisecond)

	// Ensure physical keyboard mode.
	isVirtual := page.MustEval(`() => window.__isVirtualKeyboard()`).Bool()
	require.False(t, isVirtual)

	// Open the edit form.
	page.MustEval(`(id) => window.__openEdit(id)`, msg.ID)
	editTA := page.Timeout(2 * time.Second).MustElement("#edit-form-" + msg.ID + " textarea")

	// Clear and type new text, then press Enter.
	editTA.MustSelectAllText().MustInput("edited text")
	page.Keyboard.MustType(input.Enter)

	// The edit form should close (optimistic close on successful PATCH).
	time.Sleep(500 * time.Millisecond)
	formHidden := page.MustEval(`(id) => document.getElementById('edit-form-' + id).hidden`, msg.ID).Bool()
	require.True(t, formHidden, "edit form should close after Enter submits on physical keyboard")
}
