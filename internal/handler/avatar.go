package handler

import (
	"io"
	"net/http"
	"sync"

	redisclient "github.com/emilhauk/msg/internal/redis"
)

type avatarEntry struct {
	data        []byte
	contentType string
}

// AvatarHandler proxies user avatars and caches them in-memory so browsers
// receive a stable URL with a controlled Cache-Control TTL.
type AvatarHandler struct {
	Redis *redisclient.Client
	mu    sync.Mutex
	cache map[string]*avatarEntry // userID → entry
}

func (h *AvatarHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userID")
	if userID == "" {
		http.NotFound(w, r)
		return
	}

	h.mu.Lock()
	entry := h.cache[userID]
	h.mu.Unlock()

	if entry != nil {
		h.serve(w, entry)
		return
	}

	user, err := h.Redis.GetUser(r.Context(), userID)
	if err != nil || user == nil || user.AvatarURL == "" {
		http.NotFound(w, r)
		return
	}

	resp, err := http.Get(user.AvatarURL) //nolint:noctx
	if err != nil || resp.StatusCode != http.StatusOK {
		http.NotFound(w, r)
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB max
	if err != nil {
		http.Error(w, "failed to read avatar", http.StatusBadGateway)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/jpeg"
	}

	entry = &avatarEntry{data: data, contentType: ct}
	h.mu.Lock()
	if h.cache == nil {
		h.cache = make(map[string]*avatarEntry)
	}
	h.cache[userID] = entry
	h.mu.Unlock()

	h.serve(w, entry)
}

func (h *AvatarHandler) serve(w http.ResponseWriter, e *avatarEntry) {
	w.Header().Set("Content-Type", e.contentType)
	w.Header().Set("Cache-Control", "public, max-age=300, stale-while-revalidate=86400, stale-if-error=86400")
	w.Write(e.data)
}
