package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/emilhauk/msg/internal/auth"
	"github.com/emilhauk/msg/internal/middleware"
	"github.com/emilhauk/msg/internal/model"
	redisclient "github.com/emilhauk/msg/internal/redis"
	"github.com/emilhauk/msg/internal/storage"
	"github.com/emilhauk/msg/internal/tmpl"
)

const maxAvatarBytes = 5 << 20 // 5 MiB

var avatarContentTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// ProfileHandler handles user profile management endpoints.
type ProfileHandler struct {
	Redis         *redisclient.Client
	Renderer      *tmpl.Renderer
	SessionSecret []byte
	Secure        bool
	GitHubEnabled bool
	GoogleEnabled bool
	S3            *storage.S3Client
}

type identityInfo struct {
	Provider       string
	ProviderLabel  string
	Connected      bool
	ProviderName   string // display name from the OAuth provider
	ProviderAvatar string // avatar URL from the OAuth provider
}

type profileData struct {
	Name            string
	AvatarURL       string
	Identities      []identityInfo
	CanDisconnect   bool // true if user has >1 auth method
	NameError       string
	NameSuccess     bool
	AvatarSuccess   bool
	S3Enabled       bool
	CustomAvatarURL string // clean S3 URL (form value); always set when S3 is enabled
	CustomAvatarImg string // S3 URL with cache-buster (img src for onload probe)
	IsCustomActive  bool   // true when the user's current avatar is the custom S3 upload
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

// HandleAvatarUploadURL returns a presigned S3 PUT URL for avatar upload.
// GET /user/avatar/upload-url?content_type=&content_length=
func (h *ProfileHandler) HandleAvatarUploadURL(w http.ResponseWriter, r *http.Request) {
	if h.S3 == nil {
		http.Error(w, "uploads not configured", http.StatusNotFound)
		return
	}

	user := middleware.UserFromContext(r.Context())
	q := r.URL.Query()
	contentType := q.Get("content_type")
	contentLengthStr := q.Get("content_length")

	if contentType == "" || contentLengthStr == "" {
		http.Error(w, "content_type and content_length are required", http.StatusBadRequest)
		return
	}
	if !avatarContentTypes[contentType] {
		http.Error(w, "unsupported content type; use JPEG, PNG, GIF or WebP", http.StatusBadRequest)
		return
	}

	contentLength, err := strconv.ParseInt(contentLengthStr, 10, 64)
	if err != nil || contentLength <= 0 {
		http.Error(w, "invalid content_length", http.StatusBadRequest)
		return
	}
	if contentLength > maxAvatarBytes {
		http.Error(w, fmt.Sprintf("file exceeds maximum allowed size of %d MiB", maxAvatarBytes>>20), http.StatusRequestEntityTooLarge)
		return
	}

	key := storage.AvatarKey(user.ID)

	uploadURL, err := h.S3.PresignPut(r.Context(), key, contentType, contentLength)
	if err != nil {
		http.Error(w, "failed to generate upload URL", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"upload_url": uploadURL,
		"public_url": h.S3.PublicURL(key),
	})
}

// HandleUpdateAvatar updates the user's avatar.
// Accepts provider avatar URLs or S3 avatar URLs (or empty to clear).
// PATCH /user/avatar
func (h *ProfileHandler) HandleUpdateAvatar(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	newAvatar := strings.TrimSpace(r.FormValue("avatar_url"))

	// Validate the URL is from a linked identity, a valid S3 avatar, or empty.
	isS3Avatar := false
	if newAvatar != "" {
		profiles, _ := h.Redis.GetIdentityProfiles(r.Context(), user.ID)
		valid := false
		for _, p := range profiles {
			if p.AvatarURL == newAvatar {
				valid = true
				break
			}
		}
		if !valid && h.S3 != nil {
			base := strings.SplitN(newAvatar, "?", 2)[0]
			if key, ok := h.S3.KeyFromURL(base); ok && key == storage.AvatarKey(user.ID) {
				valid = true
				isS3Avatar = true
			}
		}
		if !valid {
			http.Error(w, "invalid avatar", http.StatusBadRequest)
			return
		}
	}

	// For S3 avatars, append a random cache-buster so the stored URL changes
	// on every update, busting browser caches for all clients.
	if isS3Avatar {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		base := strings.SplitN(newAvatar, "?", 2)[0]
		newAvatar = base + "?v=" + hex.EncodeToString(b)
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

	// Build the deterministic S3 avatar URL. Existence is checked client-side
	// via img onload/onerror to avoid slow server-side S3 calls.
	var customAvatarURL, customAvatarImg string
	isCustomActive := false
	if h.S3 != nil {
		customAvatarURL = h.S3.PublicURL(storage.AvatarKey(userID))
		customAvatarImg = customAvatarURL + "?v=" + strconv.FormatInt(time.Now().UnixMilli(), 10)
		// Check if current avatar is the S3 custom avatar (strip ?v= cache-buster).
		base := strings.SplitN(avatarURL, "?", 2)[0]
		if key, ok := h.S3.KeyFromURL(base); ok && key == storage.AvatarKey(userID) {
			isCustomActive = true
		}
	}

	return profileData{
		Name:            name,
		AvatarURL:       avatarURL,
		Identities:      infoList,
		CanDisconnect:   authMethodCount > 1,
		NameError:       nameError,
		NameSuccess:     nameSuccess,
		AvatarSuccess:   avatarSuccess,
		S3Enabled:       h.S3 != nil,
		CustomAvatarURL: customAvatarURL,
		CustomAvatarImg: customAvatarImg,
		IsCustomActive:  isCustomActive,
	}
}
