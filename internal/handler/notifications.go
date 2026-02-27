package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/emilhauk/chat/internal/middleware"
	redisclient "github.com/emilhauk/chat/internal/redis"
	"github.com/emilhauk/chat/internal/webpush"
)

// NotificationsHandler handles Web Push subscription management and mute settings.
type NotificationsHandler struct {
	Redis          *redisclient.Client
	Push           *webpush.Sender
	VAPIDPublicKey string
}

// HandleVAPIDPublicKey returns the VAPID public key so clients can subscribe.
// GET /push/vapid-public-key
func (h *NotificationsHandler) HandleVAPIDPublicKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"key": h.VAPIDPublicKey}) //nolint:errcheck
}

// HandleSubscribe saves a Web Push subscription for the authenticated user.
// POST /push/subscribe  body: { endpoint, keys: { p256dh, auth } }
func (h *NotificationsHandler) HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())

	var sub struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil || sub.Endpoint == "" {
		http.Error(w, "invalid subscription", http.StatusBadRequest)
		return
	}

	// Re-encode as canonical JSON so we can feed it back to the push library.
	canonical, err := json.Marshal(map[string]any{
		"endpoint": sub.Endpoint,
		"keys": map[string]string{
			"p256dh": sub.Keys.P256dh,
			"auth":   sub.Keys.Auth,
		},
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.Redis.SavePushSubscription(r.Context(), user.ID, sub.Endpoint, string(canonical)); err != nil {
		http.Error(w, "failed to save subscription", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleUnsubscribe removes a Web Push subscription for the authenticated user.
// DELETE /push/subscribe  body: { endpoint }
func (h *NotificationsHandler) HandleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())

	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Endpoint == "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := h.Redis.DeletePushSubscription(r.Context(), user.ID, body.Endpoint); err != nil {
		http.Error(w, "failed to remove subscription", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleSetMute sets the mute duration for the authenticated user.
// POST /settings/mute  body: { duration: "1h" | "8h" | "24h" | "168h" | "forever" }
func (h *NotificationsHandler) HandleSetMute(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())

	var body struct {
		Duration string `json:"duration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	var d time.Duration
	switch body.Duration {
	case "forever", "0":
		d = 0 // indefinite
	case "1h":
		d = 1 * time.Hour
	case "8h":
		d = 8 * time.Hour
	case "24h":
		d = 24 * time.Hour
	case "168h", "1w":
		d = 168 * time.Hour
	default:
		http.Error(w, "invalid duration; use 1h, 8h, 24h, 168h, or forever", http.StatusBadRequest)
		return
	}

	if err := h.Redis.SetMute(r.Context(), user.ID, d); err != nil {
		http.Error(w, "failed to set mute", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleClearMute removes the mute for the authenticated user.
// DELETE /settings/mute
func (h *NotificationsHandler) HandleClearMute(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())
	if err := h.Redis.ClearMute(r.Context(), user.ID); err != nil {
		http.Error(w, "failed to clear mute", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleGetMute returns the current mute state for the authenticated user.
// GET /settings/mute  → { muted: bool, until?: ISO8601 | "forever" }
func (h *NotificationsHandler) HandleGetMute(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserFromContext(r.Context())

	until, isMuted, err := h.Redis.GetMuteUntil(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "failed to get mute state", http.StatusInternalServerError)
		return
	}

	resp := map[string]any{"muted": isMuted}
	if isMuted {
		// time.Date(9999,...) is our sentinel for "forever"
		if until.Year() == 9999 {
			resp["until"] = "forever"
		} else {
			resp["until"] = until.UTC().Format(time.RFC3339)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// HandleRoomMembers returns the list of room members for @mention autocomplete.
// GET /rooms/{id}/members  → [{ id, name, avatar_url }, ...]
func (h *NotificationsHandler) HandleRoomMembers(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")

	memberIDs, err := h.Redis.GetRoomMembers(r.Context(), roomID)
	if err != nil {
		http.Error(w, "failed to get members", http.StatusInternalServerError)
		return
	}

	members := make([]map[string]string, 0, len(memberIDs))
	for _, id := range memberIDs {
		u, err := h.Redis.GetUser(r.Context(), id)
		if err != nil || u == nil {
			continue
		}
		members = append(members, map[string]string{
			"id":         u.ID,
			"name":       u.Name,
			"avatar_url": u.AvatarURL,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(members) //nolint:errcheck
}
