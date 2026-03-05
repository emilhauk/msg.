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

	"github.com/emilhauk/msg/internal/middleware"
	"github.com/emilhauk/msg/internal/model"
	redisclient "github.com/emilhauk/msg/internal/redis"
	"github.com/emilhauk/msg/internal/storage"
	"github.com/emilhauk/msg/internal/tmpl"
	"github.com/emilhauk/msg/internal/webpush"
)

var urlRe = regexp.MustCompile(`https?://[^\s]+`)

// mentionRe matches @Name patterns. Names may contain letters, digits,
// spaces, hyphens and underscores (up to 64 chars). The match is
// terminated by end-of-string or a non-name character.
var mentionRe = regexp.MustCompile(`@([\w][\w\s\-]{0,62}[\w]|[\w])`)

// MessagesHandler handles message posting and history pagination.
type MessagesHandler struct {
	Redis    *redisclient.Client
	Renderer *tmpl.Renderer
	// S3 is optional. When set, attached media files are deleted from the bucket
	// alongside the message record on deletion.
	S3 *storage.S3Client
	// Push is optional. When set, Web Push notifications are sent on new messages.
	Push    *webpush.Sender
	BaseURL string
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
	// CurrentUserID is intentionally empty: the HTML is broadcast to every
	// connected client, so owner-specific controls (edit/delete buttons) must
	// not be baked in. The client applies them via JS using __currentUserID.
	html, err := h.Renderer.RenderString("message.html", model.MessageView{Message: &msg, CurrentUserID: ""})
	if err == nil {
		_ = h.Redis.Publish(r.Context(), roomID, "msg:"+html)
	}

	// Track room membership and last-active timestamp.
	go func() {
		ctx2 := context.Background()
		_ = h.Redis.TouchRoomMember(ctx2, roomID, user.ID)
		_ = h.Redis.SetRoomLastActive(ctx2, user.ID, roomID)
		_ = h.Redis.SetRoomViewing(ctx2, user.ID, roomID)
	}()

	// Async unfurl.
	if rawURL := urlRe.FindString(text); rawURL != "" {
		go h.fetchAndPublishUnfurl(rawURL, msg.ID, roomID)
	}

	// Async Web Push notifications.
	if h.Push != nil {
		mentionedNames := extractMentionedNames(text)
		go h.sendPushNotifications(msg, mentionedNames)
	}

	// Message delivered via SSE to all clients (including sender); no body needed.
	w.WriteHeader(http.StatusNoContent)
}

// extractMentionedNames returns the unique lowercased display names found in
// @mention patterns within the message text.
func extractMentionedNames(text string) []string {
	matches := mentionRe.FindAllStringSubmatch(text, -1)
	seen := make(map[string]bool)
	var names []string
	for _, m := range matches {
		name := strings.ToLower(strings.TrimSpace(m[1]))
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// sendPushNotifications delivers Web Push to all room members except the sender.
// mentionedNames is the list of lowercased display names @mentioned in the message.
func (h *MessagesHandler) sendPushNotifications(msg model.Message, mentionedNames []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	members, err := h.Redis.GetRoomMembers(ctx, msg.RoomID)
	if err != nil {
		log.Printf("webpush: get room members: %v", err)
		return
	}

	senderName := ""
	if msg.User != nil {
		senderName = msg.User.Name
	}

	mentionSet := make(map[string]bool, len(mentionedNames))
	for _, n := range mentionedNames {
		mentionSet[n] = true
	}

	roomURL := h.BaseURL + "/rooms/" + msg.RoomID

	for _, memberID := range members {
		if memberID == msg.UserID {
			continue // don't notify the sender
		}

		muted, err := h.Redis.IsMuted(ctx, memberID)
		if err != nil {
			log.Printf("webpush: check mute for %s: %v", memberID, err)
		}
		if muted {
			continue
		}

		viewing, err := h.Redis.IsRoomViewing(ctx, memberID, msg.RoomID)
		if err == nil && viewing {
			continue // user is actively viewing this room
		}

		subs, err := h.Redis.GetAllPushSubscriptions(ctx, memberID)
		if err != nil || len(subs) == 0 {
			continue
		}

		// Fetch member name to check if they were mentioned.
		member, err := h.Redis.GetUser(ctx, memberID)
		if err != nil || member == nil {
			continue
		}

		isMention := mentionSet[strings.ToLower(member.Name)]

		body := msg.Text
		if len(body) > 120 {
			body = body[:117] + "…"
		}

		title := senderName
		if isMention {
			title = senderName + " mentioned you"
		}

		payload := webpush.Payload{
			Title:     title,
			Body:      body,
			Icon:      h.BaseURL + "/static/logo_square_256.png",
			Tag:       "msg-" + msg.RoomID,
			IsMention: isMention,
			RoomID:    msg.RoomID,
			URL:       roomURL,
		}

		expired := h.Push.SendToMany(ctx, subs, payload)
		for _, endpoint := range expired {
			_ = h.Redis.DeletePushSubscription(ctx, memberID, endpoint)
		}
	}
}

// HandleHistory handles GET /rooms/{id}/messages with either:
//   - no params          — newest limit messages (reconnect catch-up)
//   - ?before=<ms>&limit=N  — paginate backwards (infinite scroll)
//   - ?after=<ms>&limit=N   — catch-up messages missed during an idle reconnect
func (h *MessagesHandler) HandleHistory(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	q := r.URL.Query()
	afterStr := q.Get("after")
	beforeStr := q.Get("before")
	limitStr := q.Get("limit")

	limit := 50
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
		limit = l
	}

	user := middleware.UserFromContext(r.Context())

	type historyData struct {
		Messages      []*model.MessageView
		RoomID        string
		OldestMS      string
		HasMore       bool
		CurrentUserID string
	}

	// No params: return the newest limit messages for reconnect catch-up.
	if afterStr == "" && beforeStr == "" {
		msgs, err := h.Redis.GetLatestMessages(r.Context(), roomID, limit)
		if err != nil {
			http.Error(w, "failed to load messages", http.StatusInternalServerError)
			return
		}
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
		h.Renderer.Render(w, http.StatusOK, "history.html", historyData{
			Messages:      views,
			RoomID:        roomID,
			OldestMS:      oldestMS,
			HasMore:       len(msgs) == limit,
			CurrentUserID: user.ID,
		})
		return
	}

	// ?after=<ms>: fetch messages newer than the given timestamp (catch-up).
	if afterStr != "" && beforeStr == "" {
		after, err := strconv.ParseInt(afterStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid after parameter", http.StatusBadRequest)
			return
		}
		msgs, err := h.Redis.GetMessagesAfter(r.Context(), roomID, after, limit)
		if err != nil {
			http.Error(w, "failed to load messages", http.StatusInternalServerError)
			return
		}
		if err := hydrateMessages(r.Context(), h.Redis, msgs, user.ID); err != nil {
			http.Error(w, "failed to load message data", http.StatusInternalServerError)
			return
		}
		views := make([]*model.MessageView, len(msgs))
		for i, m := range msgs {
			views[i] = &model.MessageView{Message: m, CurrentUserID: user.ID}
		}
		h.Renderer.Render(w, http.StatusOK, "history.html", historyData{
			Messages:      views,
			RoomID:        roomID,
			OldestMS:      "",
			HasMore:       false,
			CurrentUserID: user.ID,
		})
		return
	}

	// ?before=<ms>: paginate backwards (existing infinite-scroll behaviour).
	before, err := strconv.ParseInt(beforeStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid before parameter", http.StatusBadRequest)
		return
	}

	msgs, err := h.Redis.GetMessagesBefore(r.Context(), roomID, before, limit)
	if err != nil {
		http.Error(w, "failed to load messages", http.StatusInternalServerError)
		return
	}

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

// HandleEdit handles PATCH /rooms/{id}/messages/{msgID}.
// Only the message author may edit their own message. On success the updated
// message is re-rendered and broadcast via SSE to all clients.
func (h *MessagesHandler) HandleEdit(w http.ResponseWriter, r *http.Request) {
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

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	newText := strings.TrimSpace(r.FormValue("text"))
	if newText == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}

	if err := h.Redis.UpdateMessageText(r.Context(), msgID, newText); err != nil {
		http.Error(w, "failed to update message", http.StatusInternalServerError)
		return
	}

	// Re-fetch to get the updated text and edited_at timestamp.
	msg, err = h.Redis.GetMessage(r.Context(), msgID)
	if err != nil || msg == nil {
		http.Error(w, "failed to reload message", http.StatusInternalServerError)
		return
	}

	// Hydrate with user, unfurl, and reactions for accurate re-render.
	if err := hydrateMessages(r.Context(), h.Redis, []*model.Message{msg}, user.ID); err != nil {
		http.Error(w, "failed to hydrate message", http.StatusInternalServerError)
		return
	}

	// Re-render and broadcast neutrally (same reasoning as msg: publish above —
	// the HTML goes to all clients; the client applies owner controls via JS).
	html, err := h.Renderer.RenderString("message.html", model.MessageView{Message: msg, CurrentUserID: ""})
	if err == nil {
		_ = h.Redis.Publish(r.Context(), roomID, "edit:"+msgID+":"+html)
	}

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

	// Fetch link preview (YouTube oEmbed first, then Microlink).
	unfurl, err := fetchUnfurl(ctx, normalised)
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
