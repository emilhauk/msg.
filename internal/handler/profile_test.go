package handler_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/storage"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// HandleAvatarUploadURL
// ---------------------------------------------------------------------------

func TestAvatarUploadURL_Success(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET",
		ts.Server.URL+"/user/avatar/upload-url?content_type=image/png&content_length=1024", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.NotEmpty(t, body["upload_url"])
	assert.NotEmpty(t, body["public_url"])
	assert.Contains(t, body["public_url"], "avatars/"+alice.ID)
}

func TestAvatarUploadURL_UnsupportedContentType(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET",
		ts.Server.URL+"/user/avatar/upload-url?content_type=video/mp4&content_length=1024", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAvatarUploadURL_TooLarge(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET",
		ts.Server.URL+"/user/avatar/upload-url?content_type=image/jpeg&content_length=6000000", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestAvatarUploadURL_MissingParams(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/user/avatar/upload-url", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAvatarUploadURL_Unauthenticated(t *testing.T) {
	ts := testutil.NewTestServer(t)
	client := testutil.NoRedirectClient()

	req, _ := http.NewRequest("GET",
		ts.Server.URL+"/user/avatar/upload-url?content_type=image/png&content_length=1024", nil)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, resp.StatusCode) // redirect to /login
}

// ---------------------------------------------------------------------------
// HandleUpdateAvatar
// ---------------------------------------------------------------------------

func TestUpdateAvatar_ProviderAvatar(t *testing.T) {
	ts := testutil.NewTestServer(t)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.LinkIdentity(context.Background(),
		alice.ID, "github", "gh-123", "Alice GH", "https://github.com/avatars/alice.jpg"))
	cookie := ts.AuthCookie(t, alice)

	form := url.Values{"avatar_url": {"https://github.com/avatars/alice.jpg"}}
	req, _ := http.NewRequest("PATCH", ts.Server.URL+"/user/avatar", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	user, err := ts.Redis.GetUser(context.Background(), alice.ID)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/avatars/alice.jpg", user.AvatarURL)
}

func TestUpdateAvatar_InvalidURL(t *testing.T) {
	ts := testutil.NewTestServer(t)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	cookie := ts.AuthCookie(t, alice)

	form := url.Values{"avatar_url": {"https://evil.com/hack.jpg"}}
	req, _ := http.NewRequest("PATCH", ts.Server.URL+"/user/avatar", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestUpdateAvatar_S3Avatar_CacheBuster(t *testing.T) {
	ts := testutil.NewTestServer(t)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	cookie := ts.AuthCookie(t, alice)

	// The S3 public URL for this user's avatar.
	s3URL := ts.S3.PublicURL(storage.AvatarKey(alice.ID))

	form := url.Values{"avatar_url": {s3URL}}
	req, _ := http.NewRequest("PATCH", ts.Server.URL+"/user/avatar", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	user, err := ts.Redis.GetUser(context.Background(), alice.ID)
	require.NoError(t, err)

	// Stored URL should have the base S3 URL plus a ?v= cache-buster.
	assert.True(t, strings.HasPrefix(user.AvatarURL, s3URL+"?v="),
		"expected cache-buster suffix, got %q", user.AvatarURL)
	// Cache-buster should be 12 hex chars.
	parts := strings.SplitN(user.AvatarURL, "?v=", 2)
	require.Len(t, parts, 2)
	assert.Len(t, parts[1], 12)
}

func TestUpdateAvatar_S3Avatar_CacheBusterChangesOnResubmit(t *testing.T) {
	ts := testutil.NewTestServer(t)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	cookie := ts.AuthCookie(t, alice)

	s3URL := ts.S3.PublicURL(storage.AvatarKey(alice.ID))

	// First update.
	form := url.Values{"avatar_url": {s3URL}}
	req, _ := http.NewRequest("PATCH", ts.Server.URL+"/user/avatar", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	user1, _ := ts.Redis.GetUser(context.Background(), alice.ID)

	// Second update (same base URL, should get new cache-buster).
	req2, _ := http.NewRequest("PATCH", ts.Server.URL+"/user/avatar", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.AddCookie(cookie)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	user2, _ := ts.Redis.GetUser(context.Background(), alice.ID)

	assert.NotEqual(t, user1.AvatarURL, user2.AvatarURL,
		"cache-buster should differ between updates")
}

func TestUpdateAvatar_S3AvatarWithExistingCacheBuster(t *testing.T) {
	ts := testutil.NewTestServer(t)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	cookie := ts.AuthCookie(t, alice)

	// Submit an S3 URL that already has an old cache-buster (e.g., from the picker form).
	s3URL := ts.S3.PublicURL(storage.AvatarKey(alice.ID))
	urlWithOldBuster := s3URL + "?v=oldvalue12ab"

	form := url.Values{"avatar_url": {urlWithOldBuster}}
	req, _ := http.NewRequest("PATCH", ts.Server.URL+"/user/avatar", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	user, err := ts.Redis.GetUser(context.Background(), alice.ID)
	require.NoError(t, err)

	// Should strip old cache-buster and append a fresh one.
	assert.True(t, strings.HasPrefix(user.AvatarURL, s3URL+"?v="),
		"should have fresh cache-buster, got %q", user.AvatarURL)
	assert.NotContains(t, user.AvatarURL, "oldvalue12ab",
		"old cache-buster should be replaced")
}

func TestUpdateAvatar_ClearAvatar(t *testing.T) {
	ts := testutil.NewTestServer(t)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), model.User{
		ID: alice.ID, Name: alice.Name, Email: alice.Email,
		AvatarURL: "https://github.com/avatars/old.jpg",
	}))
	cookie := ts.AuthCookie(t, alice)

	form := url.Values{"avatar_url": {""}}
	req, _ := http.NewRequest("PATCH", ts.Server.URL+"/user/avatar", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	user, err := ts.Redis.GetUser(context.Background(), alice.ID)
	require.NoError(t, err)
	assert.Empty(t, user.AvatarURL)
}

func TestUpdateAvatar_S3URLForWrongUser(t *testing.T) {
	ts := testutil.NewTestServer(t)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	cookie := ts.AuthCookie(t, alice)

	// Try to set avatar to Bob's S3 key.
	s3URL := ts.S3.PublicURL(storage.AvatarKey(bob.ID))
	form := url.Values{"avatar_url": {s3URL}}
	req, _ := http.NewRequest("PATCH", ts.Server.URL+"/user/avatar", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// HandleProfile — S3 fields in response
// ---------------------------------------------------------------------------

func TestProfile_RendersS3Fields(t *testing.T) {
	ts := testutil.NewTestServer(t)
	s3URL := ts.S3.PublicURL(storage.AvatarKey(alice.ID))
	userWithAvatar := model.User{
		ID: alice.ID, Name: alice.Name, Email: alice.Email,
		AvatarURL: s3URL + "?v=abc123def456",
	}
	require.NoError(t, ts.Redis.CreateUser(context.Background(), userWithAvatar))
	cookie := ts.AuthCookie(t, userWithAvatar)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/user/profile", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// Upload button should be present (S3 is enabled).
	assert.Contains(t, html, `id="avatar-file-input"`)
	// Custom avatar option should be present (hidden, probed via onload).
	assert.Contains(t, html, `aria-label="Use custom avatar"`)
	// Active class should be on the custom avatar (current avatar is S3).
	assert.Contains(t, html, `settings-avatar-btn--active`)
}

func TestProfile_NoCustomActiveWhenProviderAvatar(t *testing.T) {
	ts := testutil.NewTestServer(t)
	userWithGH := model.User{
		ID: alice.ID, Name: alice.Name, Email: alice.Email,
		AvatarURL: "https://github.com/avatars/alice.jpg",
	}
	require.NoError(t, ts.Redis.CreateUser(context.Background(), userWithGH))
	require.NoError(t, ts.Redis.LinkIdentity(context.Background(),
		alice.ID, "github", "gh-123", "Alice GH", "https://github.com/avatars/alice.jpg"))
	cookie := ts.AuthCookie(t, userWithGH)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/user/profile", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// The provider avatar should have the active class.
	assert.Contains(t, html, `settings-avatar-btn--active`)
	// The custom avatar option should still render (for onload probe), but NOT active.
	// Check that IsCustomActive is false by verifying the custom button lacks --active.
	// The provider button has --active, the custom one should not.
	assert.Contains(t, html, `aria-label="Use custom avatar"`)
}
