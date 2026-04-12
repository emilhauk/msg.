package handler

import (
	"net/http"
	"strings"

	"github.com/emilhauk/msg/internal/auth"
	"github.com/emilhauk/msg/internal/middleware"
	redisclient "github.com/emilhauk/msg/internal/redis"
	"github.com/emilhauk/msg/internal/tmpl"
)

// ProfileHandler handles user profile management endpoints.
type ProfileHandler struct {
	Redis         *redisclient.Client
	Renderer      *tmpl.Renderer
	SessionSecret []byte
	Secure        bool
	GitHubEnabled bool
	GoogleEnabled bool
}

type identityInfo struct {
	Provider      string
	ProviderLabel string
	Connected     bool
}

type profileData struct {
	Name          string
	Identities    []identityInfo
	CanDisconnect bool // true if user has >1 auth method
	NameError     string
	NameSuccess   bool
}

// HandleProfile renders the profile section partial (HTMX).
// GET /user/profile
func (h *ProfileHandler) HandleProfile(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	data := h.buildProfileData(r, user.ID, user.Name, "", false)
	h.Renderer.RenderPartial(w, http.StatusOK, "profile.html", data)
}

// HandleUpdateName updates the user's display name.
// PATCH /user/profile
func (h *ProfileHandler) HandleUpdateName(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	newName := strings.TrimSpace(r.FormValue("name"))
	if newName == "" || len(newName) > 50 {
		data := h.buildProfileData(r, user.ID, user.Name, "Name must be 1–50 characters", false)
		h.Renderer.RenderPartial(w, http.StatusOK, "profile.html", data)
		return
	}

	// Atomically claim the new name index.
	oldName := user.Name
	if !strings.EqualFold(oldName, newName) {
		claimed, err := h.Redis.ClaimNameIndex(r.Context(), newName, user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !claimed {
			data := h.buildProfileData(r, user.ID, user.Name, "That name is already taken", false)
			h.Renderer.RenderPartial(w, http.StatusOK, "profile.html", data)
			return
		}
		_ = h.Redis.DeleteNameIndex(r.Context(), oldName)
	}

	if err := h.Redis.UpdateUserName(r.Context(), user.ID, newName); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := h.buildProfileData(r, user.ID, newName, "", true)
	h.Renderer.RenderPartial(w, http.StatusOK, "profile.html", data)
}

// HandleDisconnect unlinks an OAuth provider from the user's account.
// POST /user/identities/{provider}/disconnect
func (h *ProfileHandler) HandleDisconnect(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	provider := r.PathValue("provider")
	if provider != "github" && provider != "google" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	identities, err := h.Redis.GetUserIdentities(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	hasPassword, _ := h.Redis.GetUserPassword(r.Context(), user.ID)
	authMethodCount := len(identities)
	if hasPassword != "" {
		authMethodCount++
	}

	if authMethodCount <= 1 {
		data := h.buildProfileData(r, user.ID, user.Name, "", false)
		h.Renderer.RenderPartial(w, http.StatusOK, "profile.html", data)
		return
	}

	// Find the matching identity to unlink.
	found := false
	for _, ident := range identities {
		parts := strings.SplitN(ident, ":", 2)
		if len(parts) == 2 && parts[0] == provider {
			if err := h.Redis.UnlinkIdentity(r.Context(), user.ID, parts[0], parts[1]); err != nil {
				http.Error(w, "failed to disconnect provider", http.StatusInternalServerError)
				return
			}
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "provider not connected", http.StatusBadRequest)
		return
	}

	data := h.buildProfileData(r, user.ID, user.Name, "", false)
	h.Renderer.RenderPartial(w, http.StatusOK, "profile.html", data)
}

// HandleDelete permanently deletes the user's account.
// POST /user/profile/delete
func (h *ProfileHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if r.FormValue("confirmation") != "DELETE" {
		http.Error(w, "invalid confirmation", http.StatusBadRequest)
		return
	}

	if err := h.Redis.DeleteUser(r.Context(), user.ID); err != nil {
		http.Error(w, "failed to delete account", http.StatusInternalServerError)
		return
	}

	auth.ClearCookie(w, h.Secure)
	w.Header().Set("HX-Redirect", "/login")
	w.WriteHeader(http.StatusOK)
}

func (h *ProfileHandler) buildProfileData(r *http.Request, userID, name, nameError string, nameSuccess bool) profileData {
	identities, _ := h.Redis.GetUserIdentities(r.Context(), userID)
	hasPassword, _ := h.Redis.GetUserPassword(r.Context(), userID)
	authMethodCount := len(identities)
	if hasPassword != "" {
		authMethodCount++
	}

	// Build identity list with all configured providers.
	type providerDef struct {
		key     string
		label   string
		enabled bool
	}
	providers := []providerDef{
		{"github", "GitHub", h.GitHubEnabled},
		{"google", "Google", h.GoogleEnabled},
	}

	connectedSet := make(map[string]bool)
	for _, ident := range identities {
		parts := strings.SplitN(ident, ":", 2)
		if len(parts) == 2 {
			connectedSet[parts[0]] = true
		}
	}

	var infoList []identityInfo
	for _, p := range providers {
		if !p.enabled && !connectedSet[p.key] {
			continue
		}
		infoList = append(infoList, identityInfo{
			Provider:      p.key,
			ProviderLabel: p.label,
			Connected:     connectedSet[p.key],
		})
	}

	return profileData{
		Name:          name,
		Identities:    infoList,
		CanDisconnect: authMethodCount > 1,
		NameError:     nameError,
		NameSuccess:   nameSuccess,
	}
}
