package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/emilhauk/msg/internal/middleware"
	redisclient "github.com/emilhauk/msg/internal/redis"
)

// SSEHandler handles the SSE endpoint for a room.
type SSEHandler struct {
	Redis   *redisclient.Client
	Version string
}

// HandleSSE streams events to the client via Server-Sent Events.
// Payloads published to Redis are expected to be in the form:
//
//	"msg:<html>"            → event: message,  data: <html>
//	"unfurl:<id>:<html>"    → event: unfurl,   data: <id>\n<html>  (two data lines)
//	"reaction:<json>"       → event: reaction, data: <json>
//	"delete:<id>"           → event: delete,   data: <id>
//	"edit:<id>:<html>"      → event: edit,     data: <id>\n<html>  (two data lines)
//	"memberstatus:<json>"   → event: memberstatus, data: <json>
//
// For unfurl and edit the msgID and HTML are sent as separate SSE data lines so
// that the newline is the unambiguous separator on the client side. Message IDs
// contain colons (e.g. "github:12345") which would break a colon-based split.
func (h *SSEHandler) HandleSSE(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	user := middleware.UserFromContext(r.Context())

	accessible, err := h.Redis.IsRoomAccessible(r.Context(), roomID, user.ID)
	if err != nil || !accessible {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Send a comment to open the connection immediately.
	fmt.Fprint(w, ": connected\n\n")
	// Broadcast the running build version so clients can detect deploys.
	fmt.Fprintf(w, "event: version\ndata: %s\n\n", h.Version)
	flusher.Flush()

	sub := h.Redis.Subscribe(r.Context(), roomID)
	defer sub.Close()

	ch := sub.Channel()
	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case redisMsg, ok := <-ch:
			if !ok {
				return
			}
			payload := redisMsg.Payload
			switch {
			case strings.HasPrefix(payload, "version:"):
				ver := strings.TrimPrefix(payload, "version:")
				fmt.Fprintf(w, "event: version\ndata: %s\n\n", ver)
				flusher.Flush()
			case strings.HasPrefix(payload, "msg:"):
				html := strings.TrimPrefix(payload, "msg:")
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", escapeSSE(html))
				flusher.Flush()
			case strings.HasPrefix(payload, "unfurl:"):
				rest := strings.TrimPrefix(payload, "unfurl:")
				idx := strings.Index(rest, ":<")
				if idx < 0 {
					continue
				}
				msgID := rest[:idx]
				html := rest[idx+1:]
				// msgID and html are sent as two separate SSE data lines.
				// The client splits on the first \n (which SSE joins from multiple data: fields).
				fmt.Fprintf(w, "event: unfurl\ndata: %s\ndata: %s\n\n", msgID, escapeSSE(html))
				flusher.Flush()
			case strings.HasPrefix(payload, "reaction:"):
				// Payload is a JSON object; forward it as a single data line.
				jsonData := strings.TrimPrefix(payload, "reaction:")
				fmt.Fprintf(w, "event: reaction\ndata: %s\n\n", jsonData)
				flusher.Flush()
			case strings.HasPrefix(payload, "delete:"):
				msgID := strings.TrimPrefix(payload, "delete:")
				fmt.Fprintf(w, "event: delete\ndata: %s\n\n", msgID)
				flusher.Flush()
			case strings.HasPrefix(payload, "edit:"):
				rest := strings.TrimPrefix(payload, "edit:")
				idx := strings.Index(rest, ":<")
				if idx < 0 {
					continue
				}
				msgID := rest[:idx]
				html := rest[idx+1:]
				// msgID and html are sent as two separate SSE data lines.
				// The client splits on the first \n (which SSE joins from multiple data: fields).
				fmt.Fprintf(w, "event: edit\ndata: %s\ndata: %s\n\n", msgID, escapeSSE(html))
				flusher.Flush()
			case strings.HasPrefix(payload, "memberstatus:"):
			jsonData := strings.TrimPrefix(payload, "memberstatus:")
			fmt.Fprintf(w, "event: memberstatus\ndata: %s\n\n", jsonData)
			flusher.Flush()
		case strings.HasPrefix(payload, "redirect:"):
				url := strings.TrimPrefix(payload, "redirect:")
				fmt.Fprintf(w, "event: redirect\ndata: %s\n\n", url)
				flusher.Flush()
			}
		}
	}
}

// escapeSSE ensures multi-line HTML is transmitted correctly over SSE by
// prefixing each line after the first with "data: ".
func escapeSSE(html string) string {
	lines := strings.Split(html, "\n")
	return strings.Join(lines, "\ndata: ")
}
