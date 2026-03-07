//go:build !short

package browser_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setEditQuery sets the edit textarea value and dispatches an input event,
// mirroring typeQuery but targeting the edit form textarea.
func setEditQuery(page *rod.Page, text string) {
	page.MustEval(fmt.Sprintf(`() => {
		const ta = document.querySelector('.message-edit-form__textarea');
		if (!ta) return;
		ta.focus();
		ta.value = %q;
		ta.selectionStart = ta.selectionEnd = ta.value.length;
		ta.dispatchEvent(new Event('input', { bubbles: true }));
	}`, text))
}

// TestEditAutocomplete verifies that emoji shortcode and @mention autocomplete
// work inside the inline message edit textarea, not just the main compose box.
func TestEditAutocomplete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}

	const editACRoom = "room-edit-ac"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: editACRoom, Name: "Edit AC"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	ts.GrantAccess(t, editACRoom, alice.ID)

	// Post via HTTP so Alice is recorded as a room member (members ZSet).
	postMessage(t, ts, alice, editACRoom, "edit me please")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, editACRoom)
	page.Timeout(5 * time.Second).MustElement("article.message")

	// Wait for the emoji DB to be ready using the main textarea (same page).
	waitForEmojiReady(t, page)

	clearEdit := func() {
		setEditQuery(page, "")
		time.Sleep(50 * time.Millisecond)
	}

	// Open the edit form for Alice's message.
	page.MustEval(`() => {
		const btn = document.querySelector('[data-edit-trigger]');
		if (btn) btn.click();
	}`)
	page.Timeout(2 * time.Second).MustElement(".message-edit-form:not([hidden])")

	// EmojiDropdownShows: typing :smile in the edit textarea must open the
	// emoji dropdown.
	t.Run("EmojiDropdownShows", func(t *testing.T) {
		defer clearEdit()
		setEditQuery(page, ":smile")
		page.Timeout(3 * time.Second).MustElement(".emoji-autocomplete__item")

		hidden := page.MustEval(`() => document.getElementById('emoji-autocomplete').hidden`).Bool()
		assert.False(t, hidden, "emoji dropdown must appear when typing :smile in edit textarea")
	})

	// EmojiInsertedInEditTextarea: Tab must insert the emoji into the edit
	// textarea, not the main compose textarea.
	t.Run("EmojiInsertedInEditTextarea", func(t *testing.T) {
		defer clearEdit()
		setEditQuery(page, ":smile")
		page.Timeout(3 * time.Second).MustElement(".emoji-autocomplete__item")

		page.Keyboard.MustType(input.Tab)

		editVal := page.MustEval(`() => document.querySelector('.message-edit-form__textarea').value`).Str()
		assert.NotContains(t, editVal, ":", "Tab must replace :smile with emoji in the edit textarea (no colon left)")
		assert.NotEmpty(t, editVal, "edit textarea must contain the inserted emoji")

		mainVal := page.MustEval(`() => document.querySelector('.message-form__textarea').value`).Str()
		assert.Empty(t, mainVal, "main compose textarea must not be affected by autocomplete in edit form")
	})

	// EnterInsertsEmojiNotSubmit: pressing Enter with the emoji dropdown open
	// must insert the emoji and keep the edit form open (not submit it).
	t.Run("EnterInsertsEmojiNotSubmit", func(t *testing.T) {
		defer clearEdit()
		setEditQuery(page, ":smile")
		page.Timeout(3 * time.Second).MustElement(".emoji-autocomplete__item")

		page.Keyboard.MustType(input.Enter)
		time.Sleep(200 * time.Millisecond)

		// Edit form must still be visible — not closed by a premature submit.
		formHidden := page.MustEval(`() => {
			const form = document.querySelector('.message-edit-form');
			return !form || form.hidden;
		}`).Bool()
		assert.False(t, formHidden, "edit form must remain open after Enter with emoji dropdown visible")

		// Emoji dropdown must be closed after insertion.
		dropdownHidden := page.MustEval(`() => document.getElementById('emoji-autocomplete').hidden`).Bool()
		assert.True(t, dropdownHidden, "emoji dropdown must close after Enter")
	})

	// MentionDropdownShows: typing @Al in the edit textarea must show Alice in
	// the @mention dropdown.
	t.Run("MentionDropdownShows", func(t *testing.T) {
		defer clearEdit()
		setEditQuery(page, "@Al")
		time.Sleep(300 * time.Millisecond)

		hidden := page.MustEval(`() => document.getElementById('mention-autocomplete').hidden`).Bool()
		assert.False(t, hidden, "mention dropdown must appear when typing @Al in edit textarea")

		names := page.MustEval(`() => Array.from(
			document.querySelectorAll('#mention-autocomplete .emoji-autocomplete__name')
		).map(el => el.textContent.trim())`).Arr()

		found := false
		for _, v := range names {
			if strings.Contains(v.Str(), "Alice") {
				found = true
				break
			}
		}
		assert.True(t, found, "mention dropdown must show @Alice; got: %v", names)
	})

	// MentionInsertedInEditTextarea: Tab must insert the mention into the edit
	// textarea.
	t.Run("MentionInsertedInEditTextarea", func(t *testing.T) {
		defer clearEdit()
		setEditQuery(page, "@Al")
		time.Sleep(300 * time.Millisecond)
		page.Timeout(2 * time.Second).MustElement("#mention-autocomplete .emoji-autocomplete__item")

		page.Keyboard.MustType(input.Tab)

		editVal := page.MustEval(`() => document.querySelector('.message-edit-form__textarea').value`).Str()
		assert.True(t, strings.HasPrefix(editVal, "@Alice"), "Tab must insert @Alice into the edit textarea; got: %q", editVal)
	})
}
