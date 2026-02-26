package model

import "time"

// User represents an authenticated user.
type User struct {
	ID        string `redis:"id"`
	Name      string `redis:"name"`
	Email     string `redis:"email"`
	AvatarURL string `redis:"avatar_url"`
	Provider  string `redis:"provider"`
}

// Room represents a chat room.
type Room struct {
	ID   string `redis:"id"`
	Name string `redis:"name"`
}

// Message represents a single chat message.
type Message struct {
	ID        string    `redis:"id"`
	RoomID    string    `redis:"room_id"`
	UserID    string    `redis:"user_id"`
	Text      string    `redis:"text"`
	CreatedAt time.Time `redis:"-"`
	// CreatedAtMS is stored in Redis as millisecond unix timestamp string.
	CreatedAtMS string `redis:"created_at"`
	// AttachmentsJSON is a JSON-encoded []Attachment stored in Redis.
	AttachmentsJSON string `redis:"attachments"`

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
}

// Unfurl holds a cached link preview.
type Unfurl struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
	URL         string `json:"url"`
}

// MessageView wraps a Message with the ID of the currently authenticated user
// so that templates can conditionally render owner-only controls (e.g. delete).
type MessageView struct {
	*Message
	CurrentUserID string
}
