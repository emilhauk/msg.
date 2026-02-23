package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/emilhauk/chat/internal/model"
)

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
