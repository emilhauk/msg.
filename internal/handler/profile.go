package handler

import (
	"net/http"
	"strings"

	"github.com/emilhauk/msg/internal/auth"
	"github.com/emilhauk/msg/internal/middleware"
	"github.com/emilhauk/msg/internal/model"
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
	Provider       string
	ProviderLabel  string
	Connected      bool
	ProviderName   string // display name from the OAuth provider
	ProviderAvatar string // avatar URL from the OAuth provider
}

type profileData struct {
	Name          string
	AvatarURL     string
	Identities    []identityInfo
	CanDisconnect bool // true if user has >1 auth method
	NameError     string
	NameSuccess   bool
	AvatarSuccess bool
}

// HandleProfile renders the profile section partial (HTMX).
// GET /user/profile
func (h *ProfileHandler) HandleProfile(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	data := h.buildProfileData(r, user.ID, user.Name, user.AvatarURL, "", false, false)
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
		data := h.buildProfileData(r, user.ID, user.Name, user.AvatarURL, "Name must be 1–50 characters", false, false)
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
			data := h.buildProfileData(r, user.ID, user.Name, user.AvatarURL, "That name is already taken", false, false)
			h.Renderer.RenderPartial(w, http.StatusOK, "profile.html", data)
			return
		}
		_ = h.Redis.DeleteNameIndex(r.Context(), oldName)
	}

	if err := h.Redis.UpdateUserName(r.Context(), user.ID, newName); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := h.buildProfileData(r, user.ID, newName, user.AvatarURL, "", true, false)
	h.Renderer.RenderPartial(w, http.StatusOK, "profile.html", data)
}

// HandleUpdateAvatar updates the user's avatar from one of their linked provider avatars.
// PATCH /user/avatar
func (h *ProfileHandler) HandleUpdateAvatar(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	newAvatar := strings.TrimSpace(r.FormValue("avatar_url"))

	// Validate the URL is from one of the user's linked identities (or empty to clear).
	if newAvatar != "" {
		profiles, _ := h.Redis.GetIdentityProfiles(r.Context(), user.ID)
		valid := false
		for _, p := range profiles {
			if p.AvatarURL == newAvatar {
				valid = true
				break
			}
		}
		if !valid {
			http.Error(w, "invalid avatar", http.StatusBadRequest)
			return
		}
	}

	if err := h.Redis.UpdateUserAvatar(r.Context(), user.ID, newAvatar); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := h.buildProfileData(r, user.ID, user.Name, newAvatar, "", false, true)
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
		data := h.buildProfileData(r, user.ID, user.Name, user.AvatarURL, "", false, false)
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

	data := h.buildProfileData(r, user.ID, user.Name, user.AvatarURL, "", false, false)
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

func (h *ProfileHandler) buildProfileData(r *http.Request, userID, name, avatarURL, nameError string, nameSuccess, avatarSuccess bool) profileData {
	identities, _ := h.Redis.GetUserIdentities(r.Context(), userID)
	hasPassword, _ := h.Redis.GetUserPassword(r.Context(), userID)
	authMethodCount := len(identities)
	if hasPassword != "" {
		authMethodCount++
	}

	// Fetch provider-stored names and avatars for connected identities.
	profiles, _ := h.Redis.GetIdentityProfiles(r.Context(), userID)
	profileMap := make(map[string]*model.IdentityDetail, len(profiles))
	for i := range profiles {
		profileMap[profiles[i].Provider] = &profiles[i]
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
		info := identityInfo{
			Provider:      p.key,
			ProviderLabel: p.label,
			Connected:     connectedSet[p.key],
		}
		if detail, ok := profileMap[p.key]; ok {
			info.ProviderName = detail.Name
			info.ProviderAvatar = detail.AvatarURL
		}
		infoList = append(infoList, info)
	}

	return profileData{
		Name:          name,
		AvatarURL:     avatarURL,
		Identities:    infoList,
		CanDisconnect: authMethodCount > 1,
		NameError:     nameError,
		NameSuccess:   nameSuccess,
		AvatarSuccess: avatarSuccess,
	}
}
