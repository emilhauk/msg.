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

// TestMarkdown_DoubleStrikethroughRendersLineThrough verifies that ~~text~~
// also renders with text-decoration: line-through.
func TestMarkdown_DoubleStrikethroughRendersLineThrough(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-md-dstrike"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Double Strike Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessage(t, ts, alice, room, "~~double struck~~")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	del := page.Timeout(5 * time.Second).MustElement(".message__text del")
	require.Equal(t, "double struck", del.MustText())

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

// TestMarkdown_ItalicRendersEm verifies that *text* renders as <em> with
// italic font style.
func TestMarkdown_ItalicRendersEm(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-md-italic"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Italic Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessage(t, ts, alice, room, "this is *italic* text")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	em := page.Timeout(5 * time.Second).MustElement(".message__text em")
	require.Equal(t, "italic", em.MustText())

	fs := em.MustEval(`() => getComputedStyle(this).fontStyle`)
	require.Equal(t, "italic", fs.String())
}

// TestMarkdown_BoldRendersStrong verifies that **text** renders as <strong>
// with bold font weight.
func TestMarkdown_BoldRendersStrong(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-md-bold"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Bold Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessage(t, ts, alice, room, "this is **bold** text")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	strong := page.Timeout(5 * time.Second).MustElement(".message__text strong")
	require.Equal(t, "bold", strong.MustText())

	fw := strong.MustEval(`() => getComputedStyle(this).fontWeight`)
	require.Equal(t, "700", fw.String())
}

// TestMarkdown_InlineCodeRendersMonospace verifies that `text` renders as
// <code> with a monospace font family.
func TestMarkdown_InlineCodeRendersMonospace(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-md-code"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Inline Code Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessage(t, ts, alice, room, "run `go test` now")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	code := page.Timeout(5 * time.Second).MustElement(".message__text code")
	require.Equal(t, "go test", code.MustText())

	ff := code.MustEval(`() => getComputedStyle(this).fontFamily`)
	require.Contains(t, ff.String(), "monospace")
}

// TestMarkdown_UnorderedListRendersUL verifies that lines starting with "- "
// render as a <ul> with <li> items.
func TestMarkdown_UnorderedListRendersUL(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-md-ul"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "UL Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessage(t, ts, alice, room, "- first\n- second\n- third")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	ul := page.Timeout(5 * time.Second).MustElement(".message__text ul")
	items := ul.MustElements("li")
	require.Len(t, items, 3)
	require.Equal(t, "first", items[0].MustText())
	require.Equal(t, "second", items[1].MustText())
	require.Equal(t, "third", items[2].MustText())
}

// TestMarkdown_OrderedListRendersOL verifies that lines starting with "1. "
// render as an <ol> with <li> items.
func TestMarkdown_OrderedListRendersOL(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-md-ol"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "OL Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessage(t, ts, alice, room, "1. alpha\n2. bravo\n3. charlie")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	ol := page.Timeout(5 * time.Second).MustElement(".message__text ol")
	items := ol.MustElements("li")
	require.Len(t, items, 3)
	require.Equal(t, "alpha", items[0].MustText())
	require.Equal(t, "bravo", items[1].MustText())
	require.Equal(t, "charlie", items[2].MustText())
}

// TestMarkdown_FencedCodeBlockRendersPreWithHighlight verifies that fenced
// code blocks render inside a .code-block container with a <pre> and a
// language label.
func TestMarkdown_FencedCodeBlockRendersPreWithHighlight(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	const room = "room-md-fence"
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: room, Name: "Code Block Test"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))

	seedMessage(t, ts, alice, room, "```go\nfmt.Println(\"hello\")\n```")

	b := newBrowser(t)
	page := authPage(t, b, ts, alice, room)

	block := page.Timeout(5 * time.Second).MustElement(".message__text .code-block")

	// Verify language label
	lang := block.MustElement(".code-block__lang")
	require.Equal(t, "go", lang.MustText())

	// Verify code content is rendered in a <pre>
	pre := block.MustElement(".code-block__pre")
	text := pre.MustText()
	require.Contains(t, text, "Println")

	// Verify copy button exists
	block.MustElement(".code-block__copy")
}
