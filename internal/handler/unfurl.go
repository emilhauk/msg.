package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/emilhauk/msg/internal/model"
)

// extractYouTubeVideoID returns the video ID from a YouTube URL, or "" if the
// URL is not a recognised YouTube link.
func extractYouTubeVideoID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(u.Host, "www.")
	host = strings.TrimPrefix(host, "m.")
	switch host {
	case "youtu.be":
		return strings.TrimPrefix(u.Path, "/")
	case "youtube.com":
		if id := u.Query().Get("v"); id != "" {
			return id
		}
		// /shorts/VIDEO_ID or /embed/VIDEO_ID
		parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 3)
		if len(parts) >= 2 && (parts[0] == "shorts" || parts[0] == "embed") {
			return parts[1]
		}
	}
	return ""
}

// fetchYouTubeUnfurl fetches video metadata via the YouTube oEmbed API and
// returns an Unfurl with IsVideo=true. Returns nil, nil for non-YouTube URLs.
func fetchYouTubeUnfurl(ctx context.Context, rawURL string) (*model.Unfurl, error) {
	videoID := extractYouTubeVideoID(rawURL)
	if videoID == "" {
		return nil, nil
	}

	apiURL := "https://www.youtube.com/oembed?url=" + url.QueryEscape(rawURL) + "&format=json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("youtube oembed: status %d", resp.StatusCode)
	}

	var payload struct {
		Title        string `json:"title"`
		AuthorName   string `json:"author_name"`
		ThumbnailURL string `json:"thumbnail_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Title == "" {
		return nil, nil
	}

	u, _ := url.Parse(rawURL)
	isShorts := u != nil && strings.Contains(u.Path, "/shorts/")
	return &model.Unfurl{
		Title:       payload.Title,
		Description: payload.AuthorName,
		Image:       payload.ThumbnailURL,
		URL:         rawURL,
		IsVideo:     true,
		IsShorts:    isShorts,
	}, nil
}

// fetchUnfurl tries YouTube oEmbed first, then falls back to Microlink.
func fetchUnfurl(ctx context.Context, rawURL string) (*model.Unfurl, error) {
	if u, err := fetchYouTubeUnfurl(ctx, rawURL); u != nil || err != nil {
		return u, err
	}
	return fetchMicrolink(ctx, rawURL)
}

// isValidHTTPURL returns true if s is a well-formed http/https URL with a host
// and no path segments that are literally "undefined" (a Microlink data quirk).
func isValidHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	// Guard against Microlink returning paths like "/undefined".
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "undefined" {
			return false
		}
	}
	return true
}

// fetchMicrolink calls the Microlink API and returns an Unfurl, or nil if the
// URL has no usable metadata.
func fetchMicrolink(ctx context.Context, rawURL string) (*model.Unfurl, error) {
	apiURL := "https://api.microlink.io/?url=" + url.QueryEscape(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "chat-server/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("microlink: status %d", resp.StatusCode)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			URL         string `json:"url"`
			Image       struct {
				URL string `json:"url"`
			} `json:"image"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Status != "success" || payload.Data.Title == "" {
		return nil, nil
	}
	// Microlink occasionally returns malformed URLs (e.g. "https://www.youtube.com/undefined").
	// Fall back to the original URL if the returned one looks invalid.
	resolvedURL := payload.Data.URL
	if !isValidHTTPURL(resolvedURL) {
		resolvedURL = rawURL
	}

	return &model.Unfurl{
		Title:       payload.Data.Title,
		Description: payload.Data.Description,
		Image:       payload.Data.Image.URL,
		URL:         resolvedURL,
	}, nil
}
