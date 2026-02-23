// Package auth handles OAuth 2.0 login flows and session management.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/emilhauk/chat/internal/model"
	redisclient "github.com/emilhauk/chat/internal/redis"
)

// Handler holds dependencies for OAuth and session handlers.
type Handler struct {
	Redis              *redisclient.Client
	SessionSecret      []byte
	BaseURL            string
	OpenRegistration   bool
	AllowList          []string // lowercased, trimmed email addresses
	GitHubClientID     string
	GitHubClientSecret string
}

// checkAccess reports whether the given email is permitted to log in.
// When OpenRegistration is true everyone is allowed; otherwise the email must
// appear in AllowList (case-insensitive).
func (h *Handler) checkAccess(email string) bool {
	if h.OpenRegistration {
		return true
	}
	email = strings.ToLower(strings.TrimSpace(email))
	for _, allowed := range h.AllowList {
		if allowed == email {
			return true
		}
	}
	return false
}

// HandleLogin initiates the OAuth flow for the given provider.
// Only GitHub is currently supported; all other providers redirect back to /login.
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if provider != "github" {
		http.Redirect(w, r, "/login?error=unsupported_provider", http.StatusFound)
		return
	}

	state, err := generateState()
	if err != nil {
		http.Error(w, "failed to generate state", http.StatusInternalServerError)
		return
	}
	if err := h.Redis.SetOAuthState(r.Context(), state); err != nil {
		http.Error(w, "failed to store state", http.StatusInternalServerError)
		return
	}

	// Store state in a short-lived cookie so we can verify it on callback.
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600, // 10 minutes
	})

	redirectURI := h.BaseURL + "/auth/github/callback"
	authURL := fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=read%%3Auser+user%%3Aemail&state=%s",
		url.QueryEscape(h.GitHubClientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(state),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// HandleCallback handles the OAuth provider callback.
func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	if provider != "github" {
		http.Redirect(w, r, "/login?error=unsupported_provider", http.StatusFound)
		return
	}

	// Validate state to prevent CSRF.
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value == "" {
		http.Redirect(w, r, "/login?error=invalid_state", http.StatusFound)
		return
	}
	stateParam := r.URL.Query().Get("state")
	if stateParam == "" || stateParam != stateCookie.Value {
		http.Redirect(w, r, "/login?error=invalid_state", http.StatusFound)
		return
	}
	ok, err := h.Redis.ConsumeOAuthState(r.Context(), stateParam)
	if err != nil || !ok {
		http.Redirect(w, r, "/login?error=invalid_state", http.StatusFound)
		return
	}
	// Clear the state cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, "/login?error=missing_code", http.StatusFound)
		return
	}

	accessToken, err := h.exchangeGitHubCode(r.Context(), code)
	if err != nil {
		http.Redirect(w, r, "/login?error=token_exchange", http.StatusFound)
		return
	}

	user, err := h.fetchGitHubUser(r.Context(), accessToken)
	if err != nil {
		http.Redirect(w, r, "/login?error=fetch_user", http.StatusFound)
		return
	}

	if !h.checkAccess(user.Email) {
		http.Redirect(w, r, "/login?error=access_denied", http.StatusFound)
		return
	}

	if err := h.createSession(r.Context(), w, *user); err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/rooms/bemro", http.StatusFound)
}

// HandleLogout deletes the session and clears the cookie.
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	token, err := TokenFromRequest(r, h.SessionSecret)
	if err == nil {
		_ = h.Redis.DeleteSession(r.Context(), token)
	}
	ClearCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (h *Handler) createSession(ctx context.Context, w http.ResponseWriter, user model.User) error {
	if err := h.Redis.UpsertUser(ctx, user); err != nil {
		return err
	}
	signed, err := SignToken(h.SessionSecret)
	if err != nil {
		return err
	}
	// Extract the raw token portion for Redis storage.
	token, err := VerifyToken(h.SessionSecret, signed)
	if err != nil {
		return err
	}
	if err := h.Redis.SetSession(ctx, token, user); err != nil {
		return err
	}
	SetCookie(w, signed)
	return nil
}

// exchangeGitHubCode exchanges an authorization code for an access token.
func (h *Handler) exchangeGitHubCode(ctx context.Context, code string) (string, error) {
	redirectURI := h.BaseURL + "/auth/github/callback"
	body := url.Values{
		"client_id":     {h.GitHubClientID},
		"client_secret": {h.GitHubClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://github.com/login/oauth/access_token",
		strings.NewReader(body.Encode()),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf("github: %s", result.Error)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("github: empty access token")
	}
	return result.AccessToken, nil
}

// fetchGitHubUser retrieves the authenticated user's profile and primary
// verified email from the GitHub API.
func (h *Handler) fetchGitHubUser(ctx context.Context, accessToken string) (*model.User, error) {
	var ghUser struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
		Email     string `json:"email"`
	}
	if err := githubGet(ctx, accessToken, "https://api.github.com/user", &ghUser); err != nil {
		return nil, err
	}

	// Use the display name if set, fall back to the login handle.
	name := ghUser.Name
	if name == "" {
		name = ghUser.Login
	}

	// GitHub may not expose the email in /user if the user has set it private.
	// Fetch verified primary email from /user/emails.
	email := ghUser.Email
	if email == "" {
		var emails []struct {
			Email    string `json:"email"`
			Primary  bool   `json:"primary"`
			Verified bool   `json:"verified"`
		}
		if err := githubGet(ctx, accessToken, "https://api.github.com/user/emails", &emails); err != nil {
			return nil, err
		}
		for _, e := range emails {
			if e.Primary && e.Verified {
				email = e.Email
				break
			}
		}
	}

	return &model.User{
		ID:        fmt.Sprintf("github:%d", ghUser.ID),
		Name:      name,
		Email:     strings.ToLower(strings.TrimSpace(email)),
		AvatarURL: ghUser.AvatarURL,
		Provider:  "github",
	}, nil
}

// githubGet performs an authenticated GET request to the GitHub API and
// JSON-decodes the response body into dst.
func githubGet(ctx context.Context, accessToken, apiURL string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github API %s: %s", apiURL, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// generateState returns a random hex string suitable for use as an OAuth CSRF state.
func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
