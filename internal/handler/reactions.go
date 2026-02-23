package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/emilhauk/chat/internal/middleware"
	"github.com/emilhauk/chat/internal/model"
	redisclient "github.com/emilhauk/chat/internal/redis"
	"github.com/emilhauk/chat/internal/tmpl"
)

// ReactionsHandler handles emoji reactions on messages.
type ReactionsHandler struct {
	Redis    *redisclient.Client
	Renderer *tmpl.Renderer
}

// ReactionsData is the template data for reactions.html.
type ReactionsData struct {
	MsgID     string
	RoomID    string
	Reactions []model.Reaction
}

// HandleToggle handles POST /rooms/{id}/messages/{msgID}/reactions.
// It toggles the emoji reaction for the current user and broadcasts the updated
// reaction bar to all room subscribers via SSE.
func (h *ReactionsHandler) HandleToggle(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	msgID := r.PathValue("msgID")
	user := middleware.UserFromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	emoji := strings.TrimSpace(r.FormValue("emoji"))
	if emoji == "" || utf8.RuneCountInString(emoji) > 8 {
		http.Error(w, "invalid emoji", http.StatusBadRequest)
		return
	}

	// Confirm the message exists and belongs to this room.
	msg, err := h.Redis.GetMessage(r.Context(), msgID)
	if err != nil || msg == nil || msg.RoomID != roomID {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	_, err = h.Redis.ToggleReaction(r.Context(), msgID, emoji, user.ID)
	if err != nil {
		http.Error(w, "failed to update reaction", http.StatusInternalServerError)
		return
	}

	// Fetch reactions twice:
	// 1. Neutral (ReactedByMe=false for all) — used for the broadcast HTML so
	//    every client gets the same markup and applies active styling client-side.
	// 2. From the reacting user's perspective — to know which emojis they now have active.
	neutralReactions, err := h.Redis.GetReactions(r.Context(), msgID, "")
	if err != nil {
		http.Error(w, "failed to read reactions", http.StatusInternalServerError)
		return
	}

	myReactions, _ := h.Redis.GetReactions(r.Context(), msgID, user.ID)
	reactedEmojis := make([]string, 0)
	for _, rx := range myReactions {
		if rx.ReactedByMe {
			reactedEmojis = append(reactedEmojis, rx.Emoji)
		}
	}

	// Render the neutral HTML (no user-specific active state).
	data := ReactionsData{
		MsgID:     msgID,
		RoomID:    roomID,
		Reactions: neutralReactions,
	}
	html, err := h.Renderer.RenderString("reactions.html", data)
	if err != nil {
		http.Error(w, "failed to render reactions", http.StatusInternalServerError)
		return
	}

	// Publish a JSON envelope so each client can apply its own active styling.
	type reactionEvent struct {
		MsgID         string   `json:"msgId"`
		ReactorID     string   `json:"reactorId"`
		ReactedEmojis []string `json:"reactedEmojis"`
		HTML          string   `json:"html"`
	}
	payload, err := json.Marshal(reactionEvent{
		MsgID:         msgID,
		ReactorID:     user.ID,
		ReactedEmojis: reactedEmojis,
		HTML:          html,
	})
	if err == nil {
		_ = h.Redis.Publish(r.Context(), roomID, "reaction:"+string(payload))
	}

	w.WriteHeader(http.StatusNoContent)
}
