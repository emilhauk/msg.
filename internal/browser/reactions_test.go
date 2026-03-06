//go:build !short

package browser_test

import (
	"context"
	"fmt"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// TestReaction_TooltipConstrainedByMouseover verifies that hovering a reaction
// pill near the right edge of the viewport triggers constrainTooltip, keeping
// the tooltip within the viewport bounds.
func TestReaction_TooltipConstrainedByMouseover(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: reactRoom, Name: "Reaction Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	msg := seedMessage(t, ts, alice, reactRoom, "tooltip boundary test")
	postReaction(t, ts, alice, reactRoom, msg.ID, "👍")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, reactRoom)
	page.Timeout(5 * time.Second).MustElement("#reactions-" + msg.ID + " .reaction-pill")

	// Position a pill at the far right of the viewport (so its tooltip would
	// overflow without the constraint), fire mouseover to invoke constrainTooltip,
	// then verify the tooltip stays within viewport bounds.
	withinBounds := page.MustEval(fmt.Sprintf(`() => {
		const pill = document.querySelector('#reactions-%s .reaction-pill');
		if (!pill) return false;

		// Save and override styles to place the pill at the right edge.
		const savedCSSText = pill.style.cssText;
		pill.style.cssText += ';position:fixed;left:auto;right:5px;top:150px';

		// Trigger the mouseover constraint handler.
		pill.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));

		const tip = pill.querySelector('.reaction-tooltip');
		const r = tip ? tip.getBoundingClientRect() : null;

		// Restore original styles.
		pill.style.cssText = savedCSSText;

		if (!r) return true;
		return r.right <= window.innerWidth && r.left >= 0;
	}`, msg.ID)).Bool()

	assert.True(t, withinBounds, "tooltip should stay within viewport after mouseover constraint")
}

// TestReaction_PickerOpensOnClick verifies that clicking the add-reaction button
// opens the emoji picker (makes it visible).
func TestReaction_PickerOpensOnClick(t *testing.T) {
	t.Parallel()
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
