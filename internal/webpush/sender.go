// Package webpush sends Web Push notifications using VAPID authentication.
package webpush

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
	webpushlib "github.com/SherClockHolmes/webpush-go"
)

// Config holds the VAPID credentials needed to send push notifications.
type Config struct {
	// VAPIDPublicKey is the base64url-encoded uncompressed P-256 public key.
	VAPIDPublicKey string
	// VAPIDPrivateKey is the base64url-encoded P-256 private key.
	VAPIDPrivateKey string
	// VAPIDSubject is a contact URI (mailto: or https:) included in the VAPID JWT.
	VAPIDSubject string
}

// IsConfigured returns true when all three VAPID fields are non-empty.
func (c Config) IsConfigured() bool {
	return c.VAPIDPublicKey != "" && c.VAPIDPrivateKey != "" && c.VAPIDSubject != ""
}

// Payload is the JSON body delivered by the Service Worker push event.
type Payload struct {
	Action    string `json:"action,omitempty"` // "" = show notification, "clear" = dismiss
	Title     string `json:"title"`
	Body      string `json:"body"`
	Icon      string `json:"icon,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Image     string `json:"image,omitempty"`
	IsMention bool   `json:"isMention,omitempty"`
	RoomID    string `json:"roomId,omitempty"`
	URL       string `json:"url,omitempty"`
}

// Sender sends Web Push notifications.
type Sender struct {
	cfg Config
}

// New returns a Sender. Call IsConfigured() on the config before using.
func New(cfg Config) *Sender {
	return &Sender{cfg: cfg}
}

// Send delivers a push notification to a single subscription JSON string.
// It returns (expired bool, err error). expired is true when the push service
// responds with 410 Gone, meaning the subscription is no longer valid.
func (s *Sender) Send(ctx context.Context, subscriptionJSON string, p Payload) (expired bool, err error) {
	sub := &webpushlib.Subscription{}
	if err := json.Unmarshal([]byte(subscriptionJSON), sub); err != nil {
		return false, err
	}

	body, err := json.Marshal(p)
	if err != nil {
		return false, err
	}

	// webpush-go unconditionally prepends "mailto:" to any subscriber that
	// doesn't start with "https:". Strip it first so VAPID_SUBJECT values
	// like "mailto:admin@example.com" don't become "mailto:mailto:…" — a
	// malformed URI that APNs rejects with BadJwtToken while Chrome/FCM ignores.
	subscriber := strings.TrimPrefix(s.cfg.VAPIDSubject, "mailto:")

	resp, err := webpushlib.SendNotificationWithContext(ctx, body, sub, &webpushlib.Options{
		VAPIDPublicKey:  s.cfg.VAPIDPublicKey,
		VAPIDPrivateKey: s.cfg.VAPIDPrivateKey,
		Subscriber:      subscriber,
		TTL:             86400, // 24 hours
		Urgency:         webpushlib.UrgencyNormal,
	})
	if err != nil {
		// Some push services return HTTP errors as Go errors via a non-nil resp;
		// others surface them as err directly. Handle both.
		if strings.Contains(err.Error(), "410") {
			return true, nil
		}
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGone {
		return true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Ctx(ctx).Warn().
			Str("endpoint", sub.Endpoint).
			Int("status", resp.StatusCode).
			Bytes("body", bytes.TrimSpace(body)).
			Msg("webpush: push rejected")
		return false, fmt.Errorf("push service returned %d", resp.StatusCode)
	}
	return false, nil
}

// SendToMany delivers a push notification to multiple subscription JSON strings.
// It logs errors and returns a slice of expired endpoints to remove from storage.
// Runs synchronously; the caller should invoke in a goroutine for fire-and-forget use.
func (s *Sender) SendToMany(ctx context.Context, subscriptions map[string]string, p Payload) []string {
	var expired []string
	for endpoint, subJSON := range subscriptions {
		gone, err := s.Send(ctx, subJSON, p)
		if err != nil {
			log.Ctx(ctx).Error().Err(err).Str("endpoint", endpoint).Msg("webpush: send")
		}
		if gone {
			expired = append(expired, endpoint)
		}
	}
	return expired
}
