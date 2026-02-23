package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emilhauk/chat/internal/middleware"
	"github.com/emilhauk/chat/internal/model"
	redisclient "github.com/emilhauk/chat/internal/redis"
	"github.com/emilhauk/chat/internal/tmpl"
)

var urlRe = regexp.MustCompile(`https?://[^\s]+`)

// MessagesHandler handles message posting and history pagination.
type MessagesHandler struct {
	Redis    *redisclient.Client
	Renderer *tmpl.Renderer
}

// HandlePost handles POST /rooms/{id}/messages.
func (h *MessagesHandler) HandlePost(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	user := middleware.UserFromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" {
		w.WriteHeader(http.StatusOK) // no-op
		return
	}

	now := time.Now()
	msgID := fmt.Sprintf("%d-%s", now.UnixMilli(), user.ID)
	msg := model.Message{
		ID:          msgID,
		RoomID:      roomID,
		UserID:      user.ID,
		Text:        text,
		CreatedAt:   now,
		CreatedAtMS: strconv.FormatInt(now.UnixMilli(), 10),
		User:        user,
	}

	if err := h.Redis.SaveMessage(r.Context(), msg); err != nil {
		http.Error(w, "failed to save message", http.StatusInternalServerError)
		return
	}

	// Render and publish via SSE.
	html, err := h.Renderer.RenderString("message.html", msg)
	if err == nil {
		_ = h.Redis.Publish(r.Context(), roomID, "msg:"+html)
	}

	// Async unfurl.
	if rawURL := urlRe.FindString(text); rawURL != "" {
		go h.fetchAndPublishUnfurl(rawURL, msg.ID, roomID)
	}

	// Message delivered via SSE to all clients (including sender); no body needed.
	w.WriteHeader(http.StatusNoContent)
}

// HandleHistory handles GET /rooms/{id}/messages?before=<ms>&limit=50.
func (h *MessagesHandler) HandleHistory(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	beforeStr := r.URL.Query().Get("before")
	limitStr := r.URL.Query().Get("limit")

	before, err := strconv.ParseInt(beforeStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid before parameter", http.StatusBadRequest)
		return
	}
	limit := 50
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	msgs, err := h.Redis.GetMessagesBefore(r.Context(), roomID, before, limit)
	if err != nil {
		http.Error(w, "failed to load messages", http.StatusInternalServerError)
		return
	}

	user := middleware.UserFromContext(r.Context())
	if err := hydrateMessages(r.Context(), h.Redis, msgs, user.ID); err != nil {
		http.Error(w, "failed to load message data", http.StatusInternalServerError)
		return
	}

	oldestMS := ""
	if len(msgs) > 0 {
		oldestMS = msgs[0].CreatedAtMS
	}

	type historyData struct {
		Messages []*model.Message
		RoomID   string
		OldestMS string
		HasMore  bool
	}
	h.Renderer.Render(w, http.StatusOK, "history.html", historyData{
		Messages: msgs,
		RoomID:   roomID,
		OldestMS: oldestMS,
		HasMore:  len(msgs) == limit,
	})
}

// hydrateMessages fetches user, unfurl, and reaction data for a slice of
// messages in-place. currentUserID is used to set ReactedByMe on reactions.
func hydrateMessages(ctx context.Context, redis *redisclient.Client, msgs []*model.Message, currentUserID string) error {
	for _, m := range msgs {
		u, err := redis.GetUser(ctx, m.UserID)
		if err != nil {
			return err
		}
		m.User = u

		if rawURL := urlRe.FindString(m.Text); rawURL != "" {
			unfurl, err := redis.GetUnfurl(ctx, normalizeURL(rawURL))
			if err == nil {
				m.Unfurl = unfurl
			}
		}

		reactions, err := redis.GetReactions(ctx, m.ID, currentUserID)
		if err == nil {
			m.Reactions = reactions
		}
	}
	return nil
}

// fetchAndPublishUnfurl fetches a link preview and publishes an SSE event.
func (h *MessagesHandler) fetchAndPublishUnfurl(rawURL, msgID, roomID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	normalised := normalizeURL(rawURL)

	// Check cache first.
	cached, err := h.Redis.GetUnfurl(ctx, normalised)
	if err == nil && cached != nil {
		html, err := h.Renderer.RenderString("unfurl.html", unfurlData{Unfurl: cached, MsgID: msgID})
		if err == nil {
			_ = h.Redis.Publish(ctx, roomID, "unfurl:"+msgID+":"+html)
		}
		return
	}

	// Call Microlink.
	unfurl, err := fetchMicrolink(ctx, normalised)
	if err != nil || unfurl == nil {
		_ = h.Redis.SetUnfurl(ctx, normalised, nil) // cache failure
		return
	}

	_ = h.Redis.SetUnfurl(ctx, normalised, unfurl)

	html, err := h.Renderer.RenderString("unfurl.html", unfurlData{Unfurl: unfurl, MsgID: msgID})
	if err == nil {
		_ = h.Redis.Publish(ctx, roomID, "unfurl:"+msgID+":"+html)
	}
}

type unfurlData struct {
	Unfurl *model.Unfurl
	MsgID  string
}

func normalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	return u.String()
}
