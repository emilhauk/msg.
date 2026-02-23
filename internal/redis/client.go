package redis

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/emilhauk/chat/internal/model"
	goredis "github.com/redis/go-redis/v9"
)

const (
	sessionTTL = 90 * 24 * time.Hour
	messageTTL = 30 * 24 * time.Hour
	unfurlTTL  = 24 * time.Hour
	unfurlFail = 15 * time.Minute
)

// Client wraps the go-redis client with typed helpers.
type Client struct {
	rdb *goredis.Client
}

// New parses the Redis URL and returns a connected Client.
func New(redisURL string) (*Client, error) {
	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redis: parse URL: %w", err)
	}
	rdb := goredis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// Close closes the underlying Redis connection.
func (c *Client) Close() error { return c.rdb.Close() }

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

// SetSession stores session data for a given token, refreshing TTL.
func (c *Client) SetSession(ctx context.Context, token string, user model.User) error {
	key := "sessions:" + token
	pipe := c.rdb.Pipeline()
	pipe.HSet(ctx, key,
		"id", user.ID,
		"name", user.Name,
		"avatar_url", user.AvatarURL,
		"provider", user.Provider,
	)
	pipe.Expire(ctx, key, sessionTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// GetSession retrieves session data and refreshes TTL. Returns nil user if not found.
func (c *Client) GetSession(ctx context.Context, token string) (*model.User, error) {
	key := "sessions:" + token
	vals, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil || len(vals) == 0 {
		return nil, err
	}
	c.rdb.Expire(ctx, key, sessionTTL) //nolint:errcheck
	return &model.User{
		ID:        vals["id"],
		Name:      vals["name"],
		AvatarURL: vals["avatar_url"],
		Provider:  vals["provider"],
	}, nil
}

// DeleteSession removes a session.
func (c *Client) DeleteSession(ctx context.Context, token string) error {
	return c.rdb.Del(ctx, "sessions:"+token).Err()
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

// UpsertUser writes user data to Redis.
func (c *Client) UpsertUser(ctx context.Context, user model.User) error {
	return c.rdb.HSet(ctx, "users:"+user.ID,
		"id", user.ID,
		"name", user.Name,
		"avatar_url", user.AvatarURL,
		"provider", user.Provider,
	).Err()
}

// GetUser retrieves a user by ID.
func (c *Client) GetUser(ctx context.Context, id string) (*model.User, error) {
	vals, err := c.rdb.HGetAll(ctx, "users:"+id).Result()
	if err != nil || len(vals) == 0 {
		return nil, err
	}
	return &model.User{
		ID:        vals["id"],
		Name:      vals["name"],
		AvatarURL: vals["avatar_url"],
		Provider:  vals["provider"],
	}, nil
}

// ---------------------------------------------------------------------------
// Rooms
// ---------------------------------------------------------------------------

// SeedRoom creates the room if it does not already exist.
func (c *Client) SeedRoom(ctx context.Context, room model.Room) error {
	ts := float64(time.Now().Unix())
	pipe := c.rdb.Pipeline()
	pipe.ZAddNX(ctx, "rooms", goredis.Z{Score: ts, Member: room.ID})
	pipe.HSet(ctx, "rooms:"+room.ID, "id", room.ID, "name", room.Name)
	_, err := pipe.Exec(ctx)
	return err
}

// GetRoom retrieves a room by ID.
func (c *Client) GetRoom(ctx context.Context, id string) (*model.Room, error) {
	vals, err := c.rdb.HGetAll(ctx, "rooms:"+id).Result()
	if err != nil || len(vals) == 0 {
		return nil, err
	}
	return &model.Room{ID: vals["id"], Name: vals["name"]}, nil
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// SaveMessage persists a message and adds it to the room's sorted set.
func (c *Client) SaveMessage(ctx context.Context, msg model.Message) error {
	msStr := msg.CreatedAtMS
	ms, _ := strconv.ParseFloat(msStr, 64)

	pipe := c.rdb.Pipeline()
	pipe.HSet(ctx, "messages:"+msg.ID,
		"id", msg.ID,
		"room_id", msg.RoomID,
		"user_id", msg.UserID,
		"text", msg.Text,
		"created_at", msStr,
	)
	pipe.Expire(ctx, "messages:"+msg.ID, messageTTL)
	pipe.ZAdd(ctx, "rooms:"+msg.RoomID+":messages", goredis.Z{Score: ms, Member: msg.ID})

	// Evict index entries older than 30 days.
	cutoff := float64(time.Now().Add(-messageTTL).UnixMilli())
	pipe.ZRemRangeByScore(ctx, "rooms:"+msg.RoomID+":messages", "0", strconv.FormatFloat(cutoff, 'f', 0, 64))

	_, err := pipe.Exec(ctx)
	return err
}

// GetMessage retrieves a single message hash.
func (c *Client) GetMessage(ctx context.Context, id string) (*model.Message, error) {
	vals, err := c.rdb.HGetAll(ctx, "messages:"+id).Result()
	if err != nil || len(vals) == 0 {
		return nil, err
	}
	ms, _ := strconv.ParseInt(vals["created_at"], 10, 64)
	return &model.Message{
		ID:          vals["id"],
		RoomID:      vals["room_id"],
		UserID:      vals["user_id"],
		Text:        vals["text"],
		CreatedAtMS: vals["created_at"],
		CreatedAt:   time.UnixMilli(ms),
	}, nil
}

// GetLatestMessages returns up to limit messages, newest-score first, then reversed for display.
func (c *Client) GetLatestMessages(ctx context.Context, roomID string, limit int) ([]*model.Message, error) {
	ids, err := c.rdb.ZRevRange(ctx, "rooms:"+roomID+":messages", 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}
	return c.fetchMessages(ctx, ids, true)
}

// GetMessagesBefore returns up to limit messages scored below beforeMS.
func (c *Client) GetMessagesBefore(ctx context.Context, roomID string, beforeMS int64, limit int) ([]*model.Message, error) {
	max := "(" + strconv.FormatInt(beforeMS, 10)
	ids, err := c.rdb.ZRevRangeByScore(ctx, "rooms:"+roomID+":messages", &goredis.ZRangeBy{
		Max:    max,
		Min:    "-inf",
		Offset: 0,
		Count:  int64(limit),
	}).Result()
	if err != nil {
		return nil, err
	}
	return c.fetchMessages(ctx, ids, true)
}

// fetchMessages hydrates message IDs into Message structs. If reverseOrder is
// true the slice is reversed (so messages come out oldest-first).
func (c *Client) fetchMessages(ctx context.Context, ids []string, reverseOrder bool) ([]*model.Message, error) {
	msgs := make([]*model.Message, 0, len(ids))
	for _, id := range ids {
		m, err := c.GetMessage(ctx, id)
		if err != nil || m == nil {
			continue
		}
		msgs = append(msgs, m)
	}
	if reverseOrder {
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
	}
	return msgs, nil
}

// ---------------------------------------------------------------------------
// Reactions
// ---------------------------------------------------------------------------

// reactionCountsKey returns the Redis hash key that stores emoji → count for a message.
func reactionCountsKey(msgID string) string { return "reactions:" + msgID }

// reactionUsersKey returns the Redis hash key that stores "{emoji}\x00{userID}" → "1".
func reactionUsersKey(msgID string) string { return "reactions:" + msgID + ":users" }

// ToggleReaction adds or removes a single emoji reaction by userID on msgID.
// It returns the updated reaction counts as a map[emoji]count.
func (c *Client) ToggleReaction(ctx context.Context, msgID, emoji, userID string) (map[string]int, error) {
	field := emoji + "\x00" + userID
	countsKey := reactionCountsKey(msgID)
	usersKey := reactionUsersKey(msgID)

	// Check whether this user has already reacted with this emoji.
	exists, err := c.rdb.HExists(ctx, usersKey, field).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: check reaction: %w", err)
	}

	pipe := c.rdb.Pipeline()
	if exists {
		// Remove the reaction.
		pipe.HDel(ctx, usersKey, field)
		pipe.HIncrBy(ctx, countsKey, emoji, -1)
	} else {
		// Add the reaction.
		pipe.HSet(ctx, usersKey, field, 1)
		pipe.HIncrBy(ctx, countsKey, emoji, 1)
	}
	// Refresh TTLs on both keys to match messageTTL.
	pipe.Expire(ctx, countsKey, messageTTL)
	pipe.Expire(ctx, usersKey, messageTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("redis: toggle reaction: %w", err)
	}

	// Remove the key entirely if the count dropped to 0 or below.
	// HINCRBY is atomic so the value can only be 0 when we just decremented.
	if exists {
		count, err := c.rdb.HGet(ctx, countsKey, emoji).Int()
		if err == nil && count <= 0 {
			pipe2 := c.rdb.Pipeline()
			pipe2.HDel(ctx, countsKey, emoji)
			_, _ = pipe2.Exec(ctx)
		}
	}

	return c.getReactionCounts(ctx, msgID)
}

// GetReactions returns the reactions for msgID with the ReactedByMe flag set
// for the given userID.
func (c *Client) GetReactions(ctx context.Context, msgID, userID string) ([]model.Reaction, error) {
	counts, err := c.getReactionCounts(ctx, msgID)
	if err != nil {
		return nil, err
	}
	if len(counts) == 0 {
		return nil, nil
	}

	// Fetch which emojis this user has reacted with.
	usersKey := reactionUsersKey(msgID)
	userFields := make([]string, 0, len(counts))
	for emoji := range counts {
		userFields = append(userFields, emoji+"\x00"+userID)
	}
	reacted := make(map[string]bool, len(userFields))
	if userID != "" && len(userFields) > 0 {
		vals, err := c.rdb.HMGet(ctx, usersKey, userFields...).Result()
		if err == nil {
			for i, v := range vals {
				if v != nil {
					// Strip the "\x00{userID}" suffix to recover the emoji.
					emoji := userFields[i][:len(userFields[i])-len("\x00"+userID)]
					reacted[emoji] = true
				}
			}
		}
	}

	reactions := make([]model.Reaction, 0, len(counts))
	for emoji, count := range counts {
		if count > 0 {
			reactions = append(reactions, model.Reaction{
				Emoji:       emoji,
				Count:       count,
				ReactedByMe: reacted[emoji],
			})
		}
	}
	// Stable sort: by count descending, then emoji string ascending.
	for i := 1; i < len(reactions); i++ {
		for j := i; j > 0; j-- {
			a, b := reactions[j-1], reactions[j]
			if a.Count < b.Count || (a.Count == b.Count && a.Emoji > b.Emoji) {
				reactions[j-1], reactions[j] = reactions[j], reactions[j-1]
			} else {
				break
			}
		}
	}
	return reactions, nil
}

// getReactionCounts returns the raw emoji→count map for a message.
func (c *Client) getReactionCounts(ctx context.Context, msgID string) (map[string]int, error) {
	raw, err := c.rdb.HGetAll(ctx, reactionCountsKey(msgID)).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: get reaction counts: %w", err)
	}
	counts := make(map[string]int, len(raw))
	for k, v := range raw {
		n, _ := strconv.Atoi(v)
		if n > 0 {
			counts[k] = n
		}
	}
	return counts, nil
}

// ---------------------------------------------------------------------------
// OAuth state
// ---------------------------------------------------------------------------

const oauthStateTTL = 10 * time.Minute

// SetOAuthState stores a CSRF state token in Redis with a short TTL.
func (c *Client) SetOAuthState(ctx context.Context, state string) error {
	return c.rdb.Set(ctx, "oauth:state:"+state, 1, oauthStateTTL).Err()
}

// ConsumeOAuthState atomically deletes the state key and returns true if it
// existed. Returns false without error when the key is not found (expired or
// never set).
func (c *Client) ConsumeOAuthState(ctx context.Context, state string) (bool, error) {
	n, err := c.rdb.Del(ctx, "oauth:state:"+state).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ---------------------------------------------------------------------------
// Pub/Sub
// ---------------------------------------------------------------------------

// Publish sends a payload on a room's events channel.
func (c *Client) Publish(ctx context.Context, roomID, payload string) error {
	return c.rdb.Publish(ctx, "rooms:"+roomID+":events", payload).Err()
}

// Subscribe returns a PubSub subscription for a room's events channel.
func (c *Client) Subscribe(ctx context.Context, roomID string) *goredis.PubSub {
	return c.rdb.Subscribe(ctx, "rooms:"+roomID+":events")
}

// ---------------------------------------------------------------------------
// Unfurls
// ---------------------------------------------------------------------------

// unfurlKey returns the Redis key for a URL's unfurl cache.
func unfurlKey(rawURL string) string {
	h := sha256.Sum256([]byte(rawURL))
	return fmt.Sprintf("unfurls:%x", h)
}

// GetUnfurl retrieves a cached unfurl result. Returns nil if not found.
func (c *Client) GetUnfurl(ctx context.Context, rawURL string) (*model.Unfurl, error) {
	val, err := c.rdb.Get(ctx, unfurlKey(rawURL)).Result()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var u model.Unfurl
	if err := json.Unmarshal([]byte(val), &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// SetUnfurl caches an unfurl result. Pass a nil unfurl to cache a failure.
func (c *Client) SetUnfurl(ctx context.Context, rawURL string, u *model.Unfurl) error {
	ttl := unfurlTTL
	var data []byte
	var err error
	if u == nil {
		data = []byte("null")
		ttl = unfurlFail
	} else {
		data, err = json.Marshal(u)
		if err != nil {
			return err
		}
	}
	return c.rdb.Set(ctx, unfurlKey(rawURL), data, ttl).Err()
}
