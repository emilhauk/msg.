package handler

import (
	"net/http"

	"github.com/emilhauk/chat/internal/middleware"
	"github.com/emilhauk/chat/internal/model"
	redisclient "github.com/emilhauk/chat/internal/redis"
	"github.com/emilhauk/chat/internal/tmpl"
)

// RoomsHandler handles room page requests.
type RoomsHandler struct {
	Redis    *redisclient.Client
	Renderer *tmpl.Renderer
}

// HandleRoot redirects "/" to the default room.
func (h *RoomsHandler) HandleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/rooms/bemro", http.StatusFound)
}

type roomPageData struct {
	User     *model.User
	Room     *model.Room
	Messages []*model.MessageView
	// OldestMS is the created_at ms timestamp of the oldest rendered message,
	// used as the cursor for infinite scroll.
	OldestMS string
}

// HandleRoom renders the room page with the last 50 messages.
func (h *RoomsHandler) HandleRoom(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	user := middleware.UserFromContext(r.Context())

	room, err := h.Redis.GetRoom(r.Context(), roomID)
	if err != nil || room == nil {
		h.Renderer.RenderError(w, http.StatusNotFound, tmpl.ErrorData{
			User:    user,
			Title:   "Room not found",
			Message: "This room does not exist or you do not have access to it.",
		})
		return
	}

	msgs, err := h.Redis.GetLatestMessages(r.Context(), roomID, 50)
	if err != nil {
		http.Error(w, "failed to load messages", http.StatusInternalServerError)
		return
	}

	// Hydrate users, unfurls, and reactions.
	if err := hydrateMessages(r.Context(), h.Redis, msgs, user.ID); err != nil {
		http.Error(w, "failed to load message data", http.StatusInternalServerError)
		return
	}

	oldestMS := ""
	if len(msgs) > 0 {
		oldestMS = msgs[0].CreatedAtMS
	}

	views := make([]*model.MessageView, len(msgs))
	for i, m := range msgs {
		views[i] = &model.MessageView{Message: m, CurrentUserID: user.ID}
	}

	h.Renderer.Render(w, http.StatusOK, "room.html", roomPageData{
		User:     user,
		Room:     room,
		Messages: views,
		OldestMS: oldestMS,
	})
}
