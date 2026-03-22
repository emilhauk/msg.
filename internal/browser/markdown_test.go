//go:build !short

package browser_test

import (
	"context"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestMarkdown_StrikethroughRendersLineThrough verifies that ~text~ renders
// with text-decoration: line-through in the browser.
func TestMarkdown_StrikethroughRendersLineThrough(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-md-strike"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Strikethrough Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessage(t, ts, alice, room, "~struck text~")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	del := page.Timeout(5 * time.Second).MustElement(".message__text del")
	require.Equal(t, "struck text", del.MustText())

	td := del.MustEval(`() => getComputedStyle(this).textDecorationLine`)
	require.Equal(t, "line-through", td.String())
}

// TestMarkdown_BlockquoteMultiLine verifies that a multi-line blockquote
// renders over multiple visual lines (i.e. the two lines have different
// vertical positions).
func TestMarkdown_BlockquoteMultiLine(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-md-bq"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Blockquote Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessage(t, ts, alice, room, "> line one\n> line two")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	bq := page.Timeout(5 * time.Second).MustElement(".message__blockquote")
	text := bq.MustText()
	require.Contains(t, text, "line one")
	require.Contains(t, text, "line two")

	// The blockquote's rendered height should be greater than a single line,
	// proving the two lines are visually stacked (not collapsed into one).
	height := bq.MustEval(`() => this.getBoundingClientRect().height`).Num()
	lineHeight := bq.MustEval(`() => parseFloat(getComputedStyle(this).lineHeight)`).Num()
	require.Greater(t, height, lineHeight*1.5,
		"blockquote should be taller than 1.5× line-height, proving multi-line rendering")
}
