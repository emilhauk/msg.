package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emilhauk/chat/internal/middleware"
	"github.com/emilhauk/chat/internal/model"
	redisclient "github.com/emilhauk/chat/internal/redis"
	"github.com/emilhauk/chat/internal/storage"
	"github.com/emilhauk/chat/internal/tmpl"
)

var urlRe = regexp.MustCompile(`https?://[^\s]+`)

// MessagesHandler handles message posting and history pagination.
type MessagesHandler struct {
	Redis    *redisclient.Client
	Renderer *tmpl.Renderer
	// S3 is optional. When set, attached media files are deleted from the bucket
	// alongside the message record on deletion.
	S3 *storage.S3Client
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

	// Parse attachments submitted as a JSON array from the paste upload flow.
	var attachments []model.Attachment
	if raw := strings.TrimSpace(r.FormValue("attachments")); raw != "" && raw != "null" && raw != "[]" {
		if err := json.Unmarshal([]byte(raw), &attachments); err != nil {
			http.Error(w, "invalid attachments", http.StatusBadRequest)
			return
		}
		// Validate each attachment URL is non-empty (no origin check here;
		// the presign handler controls what keys are created).
		for _, a := range attachments {
			if a.URL == "" || !allowedContentTypes[a.ContentType] {
				http.Error(w, "invalid attachment", http.StatusBadRequest)
				return
			}
		}
	}

	// A message must have either text or at least one attachment.
	if text == "" && len(attachments) == 0 {
		w.WriteHeader(http.StatusOK) // no-op
		return
	}

	var attachmentsJSON string
	if len(attachments) > 0 {
		b, _ := json.Marshal(attachments)
		attachmentsJSON = string(b)
	}

	now := time.Now()
	msgID := fmt.Sprintf("%d-%s", now.UnixMilli(), user.ID)
	msg := model.Message{
		ID:              msgID,
		RoomID:          roomID,
		UserID:          user.ID,
		Text:            text,
		CreatedAt:       now,
		CreatedAtMS:     strconv.FormatInt(now.UnixMilli(), 10),
		User:            user,
		Attachments:     attachments,
		AttachmentsJSON: attachmentsJSON,
	}

	if err := h.Redis.SaveMessage(r.Context(), msg); err != nil {
		http.Error(w, "failed to save message", http.StatusInternalServerError)
		return
	}

	// Render and publish via SSE.
	// All clients receive the message via SSE; the CurrentUserID is embedded so
	// only the author sees the delete button in their own browser.
	html, err := h.Renderer.RenderString("message.html", model.MessageView{Message: &msg, CurrentUserID: user.ID})
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

	views := make([]*model.MessageView, len(msgs))
	for i, m := range msgs {
		views[i] = &model.MessageView{Message: m, CurrentUserID: user.ID}
	}

	type historyData struct {
		Messages      []*model.MessageView
		RoomID        string
		OldestMS      string
		HasMore       bool
		CurrentUserID string
	}
	h.Renderer.Render(w, http.StatusOK, "history.html", historyData{
		Messages:      views,
		RoomID:        roomID,
		OldestMS:      oldestMS,
		HasMore:       len(msgs) == limit,
		CurrentUserID: user.ID,
	})
}

// HandleDelete handles DELETE /rooms/{id}/messages/{msgID}.
// Only the message author may delete their own message. On success the message
// is removed from Redis and a "delete" SSE event is published to all clients.
func (h *MessagesHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	msgID := r.PathValue("msgID")
	user := middleware.UserFromContext(r.Context())

	msg, err := h.Redis.GetMessage(r.Context(), msgID)
	if err != nil || msg == nil {
		http.Error(w, "message not found", http.StatusNotFound)
		return
	}

	if msg.UserID != user.ID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Delete attached media from S3 before removing the message record so we
	// never leave orphaned objects if the Redis delete fails.
	if h.S3 != nil && len(msg.Attachments) > 0 {
		keys := make([]string, 0, len(msg.Attachments))
		for _, a := range msg.Attachments {
			if key, ok := h.S3.KeyFromURL(a.URL); ok {
				keys = append(keys, key)
			} else {
				log.Printf("delete message %s: attachment URL %q does not match S3 endpoint, skipping", msgID, a.URL)
			}
		}
		if err := h.S3.DeleteObjects(r.Context(), keys); err != nil {
			log.Printf("delete message %s: remove S3 objects: %v", msgID, err)
			// Non-fatal: proceed to delete the message record so the user is
			// not left unable to delete. The orphaned objects are small.
		}
	}

	if err := h.Redis.DeleteMessage(r.Context(), roomID, msgID); err != nil {
		http.Error(w, "failed to delete message", http.StatusInternalServerError)
		return
	}

	_ = h.Redis.Publish(r.Context(), roomID, "delete:"+msgID)

	w.WriteHeader(http.StatusNoContent)
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
