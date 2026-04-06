//go:build !short

package browser_test

import (
	"net/url"
	"testing"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const themeRoom = "room-theme"

// clickThemeOption opens the settings dialog and clicks the theme switcher
// button for the given value ("auto", "light", or "dark").
func clickThemeOption(page *rod.Page, value string) {
	page.MustElement("#profile-btn").MustClick()
	page.MustElement("#open-settings-btn").MustClick()
	page.MustElement("[data-theme-value=\"" + value + "\"]").MustClick()
}

// TestThemeToggle_DarkOS verifies that on a dark-mode OS the first click on the
// theme toggle jumps directly to 'light', skipping 'dark' which is invisible.
func TestThemeToggle_DarkOS(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: themeRoom, Name: "Theme Test Room"})
	ts.GrantAccess(t, themeRoom, alice.ID)

	b := newBrowser(t)
	page := b.MustPage("")

	// Emulate a dark-mode OS preference.
	require.NoError(t, proto.EmulationSetEmulatedMedia{
		Features: []*proto.EmulationMediaFeature{
			{Name: "prefers-color-scheme", Value: "dark"},
		},
	}.Call(page))

	// Clear localStorage so the page starts with data-theme="auto".
	parsed, err := url.Parse(ts.Server.URL)
	require.NoError(t, err)
	page.MustSetCookies(&proto.NetworkCookieParam{
		Name:   ts.AuthCookie(t, alice).Name,
		Value:  ts.AuthCookie(t, alice).Value,
		Domain: parsed.Hostname(),
		Path:   "/",
	})
	page.MustNavigate(ts.Server.URL + "/rooms/" + themeRoom)
	page.MustWaitLoad()
	page.MustEval(`() => localStorage.removeItem('theme')`)
	page.MustNavigate(ts.Server.URL + "/rooms/" + themeRoom)
	page.MustWaitLoad()

	// Initial state must be "auto" (no stored preference).
	initial := page.MustEval(`() => document.documentElement.getAttribute('data-theme')`).Str()
	assert.Equal(t, "auto", initial, "initial data-theme should be 'auto' with no stored preference")

	// Clicking 'light' on a dark-mode OS should set data-theme to 'light'.
	clickThemeOption(page, "light")

	theme := page.MustEval(`() => document.documentElement.getAttribute('data-theme')`).Str()
	assert.Equal(t, "light", theme, "clicking light should set data-theme to 'light'")

	stored := page.MustEval(`() => localStorage.getItem('theme')`).Str()
	assert.Equal(t, "light", stored, "localStorage should store 'light'")
}

// TestThemeToggle_LightOS verifies that on a light-mode OS the first click on
// the theme toggle goes to 'dark' (the expected visible change).
func TestThemeToggle_LightOS(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipping browser test in short mode")
	}
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: themeRoom, Name: "Theme Test Room"})
	ts.GrantAccess(t, themeRoom, alice.ID)

	b := newBrowser(t)
	page := b.MustPage("")

	// Emulate a light-mode OS preference.
	require.NoError(t, proto.EmulationSetEmulatedMedia{
		Features: []*proto.EmulationMediaFeature{
			{Name: "prefers-color-scheme", Value: "light"},
		},
	}.Call(page))

	parsed, err := url.Parse(ts.Server.URL)
	require.NoError(t, err)
	page.MustSetCookies(&proto.NetworkCookieParam{
		Name:   ts.AuthCookie(t, alice).Name,
		Value:  ts.AuthCookie(t, alice).Value,
		Domain: parsed.Hostname(),
		Path:   "/",
	})
	page.MustNavigate(ts.Server.URL + "/rooms/" + themeRoom)
	page.MustWaitLoad()
	page.MustEval(`() => localStorage.removeItem('theme')`)
	page.MustNavigate(ts.Server.URL + "/rooms/" + themeRoom)
	page.MustWaitLoad()

	// Clicking 'dark' on a light-mode OS should set data-theme to 'dark'.
	clickThemeOption(page, "dark")

	theme := page.MustEval(`() => document.documentElement.getAttribute('data-theme')`).Str()
	assert.Equal(t, "dark", theme, "clicking dark should set data-theme to 'dark'")
}
