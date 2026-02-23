package handler

import (
	"fmt"
	"net/http"
	"strings"

	redisclient "github.com/emilhauk/chat/internal/redis"
)

// SSEHandler handles the SSE endpoint for a room.
type SSEHandler struct {
	Redis *redisclient.Client
}

// HandleSSE streams events to the client via Server-Sent Events.
// Payloads published to Redis are expected to be in the form:
//
//	"msg:<html>"            → event: message
//	"unfurl:<id>:<html>"    → event: unfurl,   data: <id>:<html>
//	"reaction:<id>:<html>"  → event: reaction, data: <id>:<html>
func (h *SSEHandler) HandleSSE(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")

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
			case strings.HasPrefix(payload, "msg:"):
				html := strings.TrimPrefix(payload, "msg:")
				fmt.Fprintf(w, "event: message\ndata: %s\n\n", escapeSSE(html))
				flusher.Flush()
			case strings.HasPrefix(payload, "unfurl:"):
				rest := strings.TrimPrefix(payload, "unfurl:")
				idx := strings.Index(rest, ":")
				if idx < 0 {
					continue
				}
				msgID := rest[:idx]
				html := rest[idx+1:]
				// msgID is embedded as a prefix in data so the client can route
				// it to the correct #preview-<msgID> element.
				fmt.Fprintf(w, "event: unfurl\ndata: %s:%s\n\n", msgID, escapeSSE(html))
				flusher.Flush()
			case strings.HasPrefix(payload, "reaction:"):
				// Payload is a JSON object; forward it as a single data line.
				jsonData := strings.TrimPrefix(payload, "reaction:")
				fmt.Fprintf(w, "event: reaction\ndata: %s\n\n", jsonData)
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
