package model

import "time"

// User represents an authenticated user with a stable canonical identity.
// The ID is a UUID v4 that does not change across provider logins.
type User struct {
	ID        string `redis:"id"`
	Name      string `redis:"name"`
	Email     string `redis:"email"`
	AvatarURL string `redis:"avatar_url"`
	CreatedAt string `redis:"created_at"`
}

// Room represents a chat room.
type Room struct {
	ID   string `redis:"id"`
	Name string `redis:"name"`

	// UnreadCount is populated per-request for the sidebar; not stored in Redis.
	UnreadCount int `redis:"-"`
}

// Message represents a single chat message.
type Message struct {
	ID        string    `redis:"id"`
	RoomID    string    `redis:"room_id"`
	UserID    string    `redis:"user_id"`
	Text      string    `redis:"text"`
	Kind      string    `redis:"kind"` // "" = regular message, "system" = join/leave notification
	CreatedAt time.Time `redis:"-"`
	// CreatedAtMS is stored in Redis as millisecond unix timestamp string.
	CreatedAtMS string `redis:"created_at"`
	// AttachmentsJSON is a JSON-encoded []Attachment stored in Redis.
	AttachmentsJSON string `redis:"attachments"`
	// EditedAtMS is stored in Redis as millisecond unix timestamp string.
	// Empty for messages that have never been edited.
	EditedAtMS string `redis:"edited_at"`

	// Populated from Redis lookups or JSON decode; not stored directly.
	User        *User
	Unfurl      *Unfurl
	Reactions   []Reaction
	Attachments []Attachment
}

// Attachment represents a media file uploaded to S3 and attached to a message.
type Attachment struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Filename    string `json:"filename"`
}

// Reaction represents an emoji reaction on a message, with the current user's
// status baked in for rendering.
type Reaction struct {
	Emoji       string
	Count       int
	ReactedByMe bool
	Reactors    []User
}

// Unfurl holds a cached link preview.
type Unfurl struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
	URL         string `json:"url"`
	// IsVideo is true for video links (e.g. YouTube). The template renders a
	// 16:9 thumbnail card with a play icon affordance instead of a small square image.
	IsVideo bool `json:"is_video,omitempty"`
	// IsShorts is true for YouTube Shorts URLs (/shorts/VIDEO_ID). Implies IsVideo=true.
	// The template renders a narrow 9:16 card instead of the default 16:9 layout.
	IsShorts bool `json:"is_shorts,omitempty"`
}

// IdentityDetail holds provider-scoped identity data including the provider's
// name and avatar for the user. Assembled per-request from Redis identity hashes.
type IdentityDetail struct {
	Provider       string
	ProviderUserID string
	Name           string
	AvatarURL      string
}

// RoomMemberStatus enriches a User with room-specific presence info for the
// room panel. Assembled per-request; never stored in Redis.
type RoomMemberStatus struct {
	User            *User
	IsActive        bool // was active in this room within the last 5 minutes
	NotificationsOn bool // has push subscriptions AND is not currently muted
}

// MessageView wraps a Message with the ID of the currently authenticated user
// so that templates can conditionally render owner-only controls (e.g. delete).
type MessageView struct {
	*Message
	CurrentUserID string
}
