package redis

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/emilhauk/msg/internal/model"
	goredis "github.com/redis/go-redis/v9"
)

const (
	sessionTTL = 90 * 24 * time.Hour
	messageTTL = 30 * 24 * time.Hour
	unfurlTTL  = 24 * time.Hour
	unfurlFail = 15 * time.Minute

	redisRetryAttempts = 5
	redisRetryDelay    = 2 * time.Second
)

// Client wraps the go-redis client with typed helpers.
type Client struct {
	rdb *goredis.Client
}

// New parses the Redis URL and returns a connected Client.
// It retries the initial ping up to redisRetryAttempts times with a fixed
// delay of redisRetryDelay between attempts to accommodate slow container
// startup races.
func New(redisURL string) (*Client, error) {
	opts, err := goredis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redis: parse URL: %w", err)
	}
	rdb := goredis.NewClient(opts)

	var lastErr error
	for attempt := 1; attempt <= redisRetryAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		lastErr = rdb.Ping(ctx).Err()
		cancel()
		if lastErr == nil {
			return &Client{rdb: rdb}, nil
		}
		if attempt < redisRetryAttempts {
			log.Printf("redis: ping failed (attempt %d/%d): %v, retrying in %s...", attempt, redisRetryAttempts, lastErr, redisRetryDelay)
			time.Sleep(redisRetryDelay)
		}
	}
	return nil, fmt.Errorf("redis: ping failed after %d attempts: %w", redisRetryAttempts, lastErr)
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
	}, nil
}

// DeleteSession removes a session.
func (c *Client) DeleteSession(ctx context.Context, token string) error {
	return c.rdb.Del(ctx, "sessions:"+token).Err()
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

// CreateUser writes a new canonical user to Redis. Only called the first time
// an identity is seen. For subsequent logins, use UpsertUser to refresh
// display name and avatar.
func (c *Client) CreateUser(ctx context.Context, user model.User) error {
	return c.rdb.HSet(ctx, "users:"+user.ID,
		"id", user.ID,
		"name", user.Name,
		"avatar_url", user.AvatarURL,
		"email", user.Email,
		"created_at", user.CreatedAt,
	).Err()
}

// UpsertUser refreshes the display name and avatar for an existing canonical user.
func (c *Client) UpsertUser(ctx context.Context, user model.User) error {
	return c.rdb.HSet(ctx, "users:"+user.ID,
		"name", user.Name,
		"avatar_url", user.AvatarURL,
	).Err()
}

// GetUser retrieves a canonical user by UUID.
func (c *Client) GetUser(ctx context.Context, id string) (*model.User, error) {
	vals, err := c.rdb.HGetAll(ctx, "users:"+id).Result()
	if err != nil || len(vals) == 0 {
		return nil, err
	}
	return &model.User{
		ID:        vals["id"],
		Name:      vals["name"],
		AvatarURL: vals["avatar_url"],
		Email:     vals["email"],
		CreatedAt: vals["created_at"],
	}, nil
}

// GetUsers retrieves multiple users by UUID in a single pipeline. Missing or
// empty results are silently skipped. The returned map may have fewer entries
// than ids when some are not found.
func (c *Client) GetUsers(ctx context.Context, ids []string) (map[string]*model.User, error) {
	if len(ids) == 0 {
		return map[string]*model.User{}, nil
	}
	pipe := c.rdb.Pipeline()
	cmds := make([]*goredis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGetAll(ctx, "users:"+id)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != goredis.Nil {
		return nil, fmt.Errorf("redis: get users pipeline: %w", err)
	}
	result := make(map[string]*model.User, len(ids))
	for i, cmd := range cmds {
		vals, err := cmd.Result()
		if err != nil || len(vals) == 0 {
			continue
		}
		result[ids[i]] = &model.User{
			ID:        vals["id"],
			Name:      vals["name"],
			AvatarURL: vals["avatar_url"],
		}
	}
	return result, nil
}

// GetUserByIdentity looks up the canonical user UUID for a given OAuth identity
// (provider + provider-scoped user ID), then retrieves the full user record.
// Returns nil without error when the identity has not been registered before.
func (c *Client) GetUserByIdentity(ctx context.Context, provider, providerUserID string) (*model.User, error) {
	canonicalID, err := c.rdb.Get(ctx, "identities:"+provider+":"+providerUserID).Result()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis: get identity: %w", err)
	}
	return c.GetUser(ctx, canonicalID)
}

// GetUserByEmail looks up the canonical user UUID stored in the email index,
// then retrieves the full user record. Returns nil without error when no user
// with that email address has been registered via password auth.
func (c *Client) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	canonicalID, err := c.rdb.Get(ctx, "email_index:"+email).Result()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis: get email index: %w", err)
	}
	return c.GetUser(ctx, canonicalID)
}

// SetEmailIndex writes email_index:{email} → userID. This is used exclusively
// by password-auth account creation so that users can be looked up by email at
// login time.
func (c *Client) SetEmailIndex(ctx context.Context, email, userID string) error {
	return c.rdb.Set(ctx, "email_index:"+email, userID, 0).Err()
}

// SetUserPassword stores a bcrypt hash for the given user.
func (c *Client) SetUserPassword(ctx context.Context, userID, bcryptHash string) error {
	return c.rdb.Set(ctx, "users:"+userID+":password", bcryptHash, 0).Err()
}

// GetUserPassword retrieves the bcrypt hash for the given user. Returns an
// empty string without error when the user has no password set (OAuth-only account).
func (c *Client) GetUserPassword(ctx context.Context, userID string) (string, error) {
	val, err := c.rdb.Get(ctx, "users:"+userID+":password").Result()
	if err == goredis.Nil {
		return "", nil
	}
	return val, err
}

// LinkIdentity atomically records that provider:providerUserID maps to the
// canonical userID and adds the identity string to the user's identity set.
func (c *Client) LinkIdentity(ctx context.Context, userID, provider, providerUserID string) error {
	identityKey := "identities:" + provider + ":" + providerUserID
	identitiesSetKey := "users:" + userID + ":identities"
	identityMember := provider + ":" + providerUserID

	pipe := c.rdb.Pipeline()
	pipe.Set(ctx, identityKey, userID, 0) // no TTL — identity mappings are permanent
	pipe.SAdd(ctx, identitiesSetKey, identityMember)
	_, err := pipe.Exec(ctx)
	return err
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
		"attachments", msg.AttachmentsJSON,
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
	msg := &model.Message{
		ID:              vals["id"],
		RoomID:          vals["room_id"],
		UserID:          vals["user_id"],
		Text:            vals["text"],
		CreatedAtMS:     vals["created_at"],
		CreatedAt:       time.UnixMilli(ms),
		AttachmentsJSON: vals["attachments"],
		EditedAtMS:      vals["edited_at"],
	}
	if msg.AttachmentsJSON != "" && msg.AttachmentsJSON != "null" {
		_ = json.Unmarshal([]byte(msg.AttachmentsJSON), &msg.Attachments)
	}
	return msg, nil
}

// GetLatestMessages returns up to limit messages, newest-score first, then reversed for display.
func (c *Client) GetLatestMessages(ctx context.Context, roomID string, limit int) ([]*model.Message, error) {
	ids, err := c.rdb.ZRevRange(ctx, "rooms:"+roomID+":messages", 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}
	return c.fetchMessages(ctx, ids, true)
}

// GetMessagesAfter returns up to limit messages scored above afterMS, oldest-first.
func (c *Client) GetMessagesAfter(ctx context.Context, roomID string, afterMS int64, limit int) ([]*model.Message, error) {
	min := "(" + strconv.FormatInt(afterMS, 10)
	ids, err := c.rdb.ZRangeByScore(ctx, "rooms:"+roomID+":messages", &goredis.ZRangeBy{
		Min:   min,
		Max:   "+inf",
		Count: int64(limit),
	}).Result()
	if err != nil {
		return nil, err
	}
	return c.fetchMessages(ctx, ids, false) // already ascending; no reversal needed
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

// UpdateMessageText updates a message's text and records the edit timestamp.
// The message TTL is not reset — it was set at creation and remains unchanged.
func (c *Client) UpdateMessageText(ctx context.Context, msgID, newText string) error {
	editedAtMS := strconv.FormatInt(time.Now().UnixMilli(), 10)
	return c.rdb.HSet(ctx, "messages:"+msgID,
		"text", newText,
		"edited_at", editedAtMS,
	).Err()
}

// DeleteMessage removes a message hash, its sorted-set entry, and any reactions.
func (c *Client) DeleteMessage(ctx context.Context, roomID, msgID string) error {
	pipe := c.rdb.Pipeline()
	pipe.Del(ctx, "messages:"+msgID)
	pipe.ZRem(ctx, "rooms:"+roomID+":messages", msgID)
	pipe.Del(ctx, "reactions:"+msgID)
	pipe.Del(ctx, "reactions:"+msgID+":users")
	_, err := pipe.Exec(ctx)
	return err
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

	// Fetch reactor user IDs per emoji.
	allUserFields, err := c.rdb.HGetAll(ctx, usersKey).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: get reaction users: %w", err)
	}
	emojiReactors := make(map[string][]string) // emoji → []userID
	uniqueIDs := make(map[string]struct{})
	for field := range allUserFields {
		sep := len(field) - 36 - 1 // emoji\x00uuid (uuid is 36 chars)
		if sep < 0 {
			continue
		}
		// field format: "{emoji}\x00{userID}"
		nulIdx := -1
		for i := len(field) - 1; i >= 0; i-- {
			if field[i] == 0 {
				nulIdx = i
				break
			}
		}
		if nulIdx < 0 {
			continue
		}
		emoji, uid := field[:nulIdx], field[nulIdx+1:]
		emojiReactors[emoji] = append(emojiReactors[emoji], uid)
		uniqueIDs[uid] = struct{}{}
	}
	allUniqueIDs := make([]string, 0, len(uniqueIDs))
	for uid := range uniqueIDs {
		allUniqueIDs = append(allUniqueIDs, uid)
	}
	users, err := c.GetUsers(ctx, allUniqueIDs)
	if err != nil {
		return nil, err
	}

	reactions := make([]model.Reaction, 0, len(counts))
	for emoji, count := range counts {
		if count > 0 {
			reactors := make([]model.User, 0, len(emojiReactors[emoji]))
			for _, uid := range emojiReactors[emoji] {
				if u, ok := users[uid]; ok {
					reactors = append(reactors, *u)
				}
			}
			reactions = append(reactions, model.Reaction{
				Emoji:       emoji,
				Count:       count,
				ReactedByMe: reacted[emoji],
				Reactors:    reactors,
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
// Room members
// ---------------------------------------------------------------------------

// TouchRoomMember records that userID is a member of roomID, scoring by
// current unix milliseconds so the most-recently-active user has the highest score.
func (c *Client) TouchRoomMember(ctx context.Context, roomID, userID string) error {
	score := float64(time.Now().UnixMilli())
	return c.rdb.ZAdd(ctx, "rooms:"+roomID+":members", goredis.Z{Score: score, Member: userID}).Err()
}

// GetRoomMembers returns all user IDs that have ever posted in the room,
// ordered by most-recently-active first.
func (c *Client) GetRoomMembers(ctx context.Context, roomID string) ([]string, error) {
	return c.rdb.ZRevRange(ctx, "rooms:"+roomID+":members", 0, -1).Result()
}

// ---------------------------------------------------------------------------
// Push subscriptions
// ---------------------------------------------------------------------------

// SavePushSubscription stores a Web Push subscription for a user.
// The subscription JSON is keyed by its endpoint URL (which is unique per browser/device).
func (c *Client) SavePushSubscription(ctx context.Context, userID, endpoint, subscriptionJSON string) error {
	return c.rdb.HSet(ctx, "users:"+userID+":push_subscriptions", endpoint, subscriptionJSON).Err()
}

// DeletePushSubscription removes a specific push subscription by endpoint.
func (c *Client) DeletePushSubscription(ctx context.Context, userID, endpoint string) error {
	return c.rdb.HDel(ctx, "users:"+userID+":push_subscriptions", endpoint).Err()
}

// GetPushSubscriptions returns all push subscription JSON strings for a user.
func (c *Client) GetPushSubscriptions(ctx context.Context, userID string) ([]string, error) {
	vals, err := c.rdb.HVals(ctx, "users:"+userID+":push_subscriptions").Result()
	if err != nil {
		return nil, err
	}
	return vals, nil
}

// GetAllPushSubscriptions returns a map of endpoint → subscriptionJSON for a user.
func (c *Client) GetAllPushSubscriptions(ctx context.Context, userID string) (map[string]string, error) {
	return c.rdb.HGetAll(ctx, "users:"+userID+":push_subscriptions").Result()
}

// ---------------------------------------------------------------------------
// Mute / DND
// ---------------------------------------------------------------------------

const muteForever = "forever"

// SetMute sets a mute duration for a user. Pass 0 for indefinite mute.
func (c *Client) SetMute(ctx context.Context, userID string, duration time.Duration) error {
	key := "users:" + userID + ":mute_until"
	if duration == 0 {
		// Indefinite: store sentinel value with no TTL.
		return c.rdb.Set(ctx, key, muteForever, 0).Err()
	}
	until := strconv.FormatInt(time.Now().Add(duration).UnixMilli(), 10)
	return c.rdb.Set(ctx, key, until, duration).Err()
}

// ClearMute removes the mute for a user.
func (c *Client) ClearMute(ctx context.Context, userID string) error {
	return c.rdb.Del(ctx, "users:"+userID+":mute_until").Err()
}

// IsMuted returns true if the user is currently muted.
func (c *Client) IsMuted(ctx context.Context, userID string) (bool, error) {
	val, err := c.rdb.Get(ctx, "users:"+userID+":mute_until").Result()
	if err == goredis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if val == muteForever {
		return true, nil
	}
	// Parse as unix ms timestamp.
	ms, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return false, nil
	}
	return time.Now().UnixMilli() < ms, nil
}

// GetMuteUntil returns when the mute expires. Returns zero time if not muted,
// and time.Time{} with isMuted=false if expired.
// Returns (time.Time{Max}, true) for indefinite mutes.
func (c *Client) GetMuteUntil(ctx context.Context, userID string) (until time.Time, isMuted bool, err error) {
	val, err := c.rdb.Get(ctx, "users:"+userID+":mute_until").Result()
	if err == goredis.Nil {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if val == muteForever {
		return time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), true, nil
	}
	ms, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return time.Time{}, false, nil
	}
	t := time.UnixMilli(ms)
	if time.Now().After(t) {
		return time.Time{}, false, nil
	}
	return t, true, nil
}

// ---------------------------------------------------------------------------
// Room activity (last_active / read cursor)
// ---------------------------------------------------------------------------

// SetRoomLastActive records the current time as the user's last-active timestamp
// for a room. TTL is 30 days (matching message TTL) so the value survives after
// the session ends and can be used for future unread-count work.
func (c *Client) SetRoomLastActive(ctx context.Context, userID, roomID string) error {
	key := "users:" + userID + ":rooms:" + roomID + ":last_active"
	val := strconv.FormatInt(time.Now().UnixMilli(), 10)
	return c.rdb.Set(ctx, key, val, messageTTL).Err()
}

// GetRoomLastActive retrieves the last-active timestamp for a user in a room.
// Returns (zero, false, nil) when no record exists.
func (c *Client) GetRoomLastActive(ctx context.Context, userID, roomID string) (time.Time, bool, error) {
	key := "users:" + userID + ":rooms:" + roomID + ":last_active"
	val, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, goredis.Nil) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	ms, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return time.Time{}, false, err
	}
	return time.UnixMilli(ms), true, nil
}

// SetRoomViewing records that the user is actively viewing the room.
// The key expires after roomViewingTTL; the heartbeat must refresh it to keep
// it alive. When the user leaves (visibilitychange → hidden) the client sends
// a beacon to clear the key immediately.
const roomViewingTTL = 90 * time.Second

func (c *Client) SetRoomViewing(ctx context.Context, userID, roomID string) error {
	key := "users:" + userID + ":rooms:" + roomID + ":viewing"
	return c.rdb.Set(ctx, key, "1", roomViewingTTL).Err()
}

func (c *Client) ClearRoomViewing(ctx context.Context, userID, roomID string) error {
	key := "users:" + userID + ":rooms:" + roomID + ":viewing"
	return c.rdb.Del(ctx, key).Err()
}

func (c *Client) IsRoomViewing(ctx context.Context, userID, roomID string) (bool, error) {
	key := "users:" + userID + ":rooms:" + roomID + ":viewing"
	exists, err := c.rdb.Exists(ctx, key).Result()
	return exists > 0, err
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
