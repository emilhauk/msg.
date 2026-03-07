package redis_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/emilhauk/msg/internal/model"
	redisclient "github.com/emilhauk/msg/internal/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClient(t *testing.T) (*redisclient.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc, err := redisclient.New("redis://" + mr.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { rc.Close() })
	return rc, mr
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

func TestSetGetSession_RoundTrip(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()

	user := model.User{ID: "u1", Name: "Alice", Email: "alice@example.com", AvatarURL: "https://example.com/a.png"}
	require.NoError(t, rc.SetSession(ctx, "token1", user))

	got, err := rc.GetSession(ctx, "token1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, user.ID, got.ID)
	assert.Equal(t, user.Name, got.Name)
	assert.Equal(t, user.AvatarURL, got.AvatarURL)
}

func TestGetSession_Missing(t *testing.T) {
	rc, _ := newClient(t)
	got, err := rc.GetSession(context.Background(), "nonexistent-token")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSessionTTL_FastForward(t *testing.T) {
	rc, mr := newClient(t)
	ctx := context.Background()

	user := model.User{ID: "u2", Name: "Bob"}
	require.NoError(t, rc.SetSession(ctx, "token2", user))

	// Advance time past the 90-day session TTL.
	mr.FastForward(91 * 24 * time.Hour)

	got, err := rc.GetSession(ctx, "token2")
	require.NoError(t, err)
	assert.Nil(t, got, "session should have expired after 91 days")
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

func TestMessagePagination(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()
	roomID := "test-room"

	// Seed 5 messages with increasing timestamps.
	base := time.Now().UnixMilli()
	var msgs []model.Message
	for i := 0; i < 5; i++ {
		ms := base + int64(i*1000)
		msg := model.Message{
			ID:          fmt.Sprintf("%d-user1", ms),
			RoomID:      roomID,
			UserID:      "user1",
			Text:        fmt.Sprintf("msg %d", i),
			CreatedAt:   time.UnixMilli(ms),
			CreatedAtMS: fmt.Sprintf("%d", ms),
		}
		require.NoError(t, rc.SaveMessage(ctx, msg))
		msgs = append(msgs, msg)
	}

	t.Run("GetLatestMessages", func(t *testing.T) {
		got, err := rc.GetLatestMessages(ctx, roomID, 10)
		require.NoError(t, err)
		assert.Len(t, got, 5)
		// Should be oldest-first (ascending).
		assert.Equal(t, msgs[0].ID, got[0].ID)
		assert.Equal(t, msgs[4].ID, got[4].ID)
	})

	t.Run("GetMessagesBefore", func(t *testing.T) {
		// Before msg[3]: should return msgs[0..2].
		got, err := rc.GetMessagesBefore(ctx, roomID, msgs[3].CreatedAt.UnixMilli(), 10)
		require.NoError(t, err)
		assert.Len(t, got, 3)
		assert.Equal(t, msgs[0].ID, got[0].ID)
		assert.Equal(t, msgs[2].ID, got[2].ID)
	})

	t.Run("GetMessagesAfter", func(t *testing.T) {
		// After msg[1]: should return msgs[2..4].
		got, err := rc.GetMessagesAfter(ctx, roomID, msgs[1].CreatedAt.UnixMilli(), 10)
		require.NoError(t, err)
		assert.Len(t, got, 3)
		assert.Equal(t, msgs[2].ID, got[0].ID)
		assert.Equal(t, msgs[4].ID, got[2].ID)
	})

	t.Run("GetLatestMessages_Limit", func(t *testing.T) {
		got, err := rc.GetLatestMessages(ctx, roomID, 2)
		require.NoError(t, err)
		assert.Len(t, got, 2)
		// Should return the two newest messages.
		assert.Equal(t, msgs[3].ID, got[0].ID)
		assert.Equal(t, msgs[4].ID, got[1].ID)
	})
}

// ---------------------------------------------------------------------------
// Reactions
// ---------------------------------------------------------------------------

func TestReactionToggle(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()

	roomID := "rr"
	ms := time.Now().UnixMilli()
	msg := model.Message{
		ID:          fmt.Sprintf("%d-user1", ms),
		RoomID:      roomID,
		UserID:      "user1",
		Text:        "react!",
		CreatedAt:   time.UnixMilli(ms),
		CreatedAtMS: fmt.Sprintf("%d", ms),
	}
	require.NoError(t, rc.SaveMessage(ctx, msg))

	t.Run("add reaction", func(t *testing.T) {
		counts, err := rc.ToggleReaction(ctx, msg.ID, "👍", "user1")
		require.NoError(t, err)
		assert.Equal(t, 1, counts["👍"])
	})

	t.Run("second user adds same reaction", func(t *testing.T) {
		counts, err := rc.ToggleReaction(ctx, msg.ID, "👍", "user2")
		require.NoError(t, err)
		assert.Equal(t, 2, counts["👍"])
	})

	t.Run("first user removes reaction", func(t *testing.T) {
		counts, err := rc.ToggleReaction(ctx, msg.ID, "👍", "user1")
		require.NoError(t, err)
		assert.Equal(t, 1, counts["👍"])
	})

	t.Run("last user removes reaction - count drops to zero", func(t *testing.T) {
		counts, err := rc.ToggleReaction(ctx, msg.ID, "👍", "user2")
		require.NoError(t, err)
		// Count of 0 should be omitted from the map.
		_, exists := counts["👍"]
		assert.False(t, exists, "emoji key should be removed when count reaches 0")
	})
}

// ---------------------------------------------------------------------------
// Push subscriptions
// ---------------------------------------------------------------------------

func TestPushSubscription_CRUD(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()
	userID := "push-user"
	endpoint := "https://push.example.com/sub/abc123"
	subJSON := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"abc","auth":"def"}}`

	// Save subscription.
	require.NoError(t, rc.SavePushSubscription(ctx, userID, endpoint, subJSON))

	// GetAll should have it.
	subs, err := rc.GetAllPushSubscriptions(ctx, userID)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, subJSON, subs[endpoint])

	// Delete subscription.
	require.NoError(t, rc.DeletePushSubscription(ctx, userID, endpoint))

	// GetAll should now be empty.
	subs, err = rc.GetAllPushSubscriptions(ctx, userID)
	require.NoError(t, err)
	assert.Empty(t, subs)
}

// ---------------------------------------------------------------------------
// Mute / DND
// ---------------------------------------------------------------------------

func TestMute_Timed(t *testing.T) {
	rc, mr := newClient(t)
	ctx := context.Background()
	userID := "mute-timed"

	require.NoError(t, rc.SetMute(ctx, userID, time.Hour))

	muted, err := rc.IsMuted(ctx, userID)
	require.NoError(t, err)
	assert.True(t, muted, "should be muted immediately after SetMute")

	// Advance time past the 1-hour TTL; key expires in miniredis.
	mr.FastForward(2 * time.Hour)

	muted, err = rc.IsMuted(ctx, userID)
	require.NoError(t, err)
	assert.False(t, muted, "should not be muted after TTL expires")
}

func TestMute_Forever(t *testing.T) {
	rc, mr := newClient(t)
	ctx := context.Background()
	userID := "mute-forever"

	require.NoError(t, rc.SetMute(ctx, userID, 0)) // 0 = indefinite

	muted, err := rc.IsMuted(ctx, userID)
	require.NoError(t, err)
	assert.True(t, muted)

	// Advancing time must not expire an indefinite mute.
	mr.FastForward(1000 * time.Hour)

	muted, err = rc.IsMuted(ctx, userID)
	require.NoError(t, err)
	assert.True(t, muted, "indefinite mute should persist after FastForward")
}

func TestMute_Clear(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()
	userID := "mute-clear"

	require.NoError(t, rc.SetMute(ctx, userID, time.Hour))
	require.NoError(t, rc.ClearMute(ctx, userID))

	muted, err := rc.IsMuted(ctx, userID)
	require.NoError(t, err)
	assert.False(t, muted, "mute should be cleared")
}

func TestGetMuteUntil_NotMuted(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()

	until, isMuted, err := rc.GetMuteUntil(ctx, "no-mute-user")
	require.NoError(t, err)
	assert.False(t, isMuted)
	assert.True(t, until.IsZero())
}

func TestGetMuteUntil_Forever(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()
	userID := "mute-until-forever"

	require.NoError(t, rc.SetMute(ctx, userID, 0))

	until, isMuted, err := rc.GetMuteUntil(ctx, userID)
	require.NoError(t, err)
	assert.True(t, isMuted)
	assert.Equal(t, 9999, until.Year(), "sentinel year for indefinite mute should be 9999")
}

func TestGetMuteUntil_Timed(t *testing.T) {
	rc, mr := newClient(t)
	ctx := context.Background()
	userID := "mute-until-timed"

	require.NoError(t, rc.SetMute(ctx, userID, time.Hour))

	until, isMuted, err := rc.GetMuteUntil(ctx, userID)
	require.NoError(t, err)
	assert.True(t, isMuted)
	assert.False(t, until.IsZero(), "until should be a non-zero time")
	assert.True(t, until.After(time.Now()), "until should be in the future")

	// After TTL expiry the key is gone — GetMuteUntil should report not muted.
	mr.FastForward(2 * time.Hour)

	_, isMuted, err = rc.GetMuteUntil(ctx, userID)
	require.NoError(t, err)
	assert.False(t, isMuted, "should not be muted after TTL expires")
}

// ---------------------------------------------------------------------------
// Rooms
// ---------------------------------------------------------------------------

func TestCreateRoom_UniqueID(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()

	room, err := rc.CreateRoom(ctx, "Test Room", "creator-1")
	require.NoError(t, err)
	assert.Len(t, room.ID, 8, "room ID should be 8 hex chars")
	assert.Equal(t, "Test Room", room.Name)

	// Creator should have access.
	ok, err := rc.IsRoomAccessible(ctx, room.ID, "creator-1")
	require.NoError(t, err)
	assert.True(t, ok, "creator should have access")
}

func TestCreateRoom_CollisionRetry(t *testing.T) {
	rc, mr := newClient(t)
	ctx := context.Background()

	// Pre-create a room to ensure the hash exists. We can't easily control
	// the random ID, but we can verify that CreateRoom never overwrites an
	// existing room by seeding all 8-char hex IDs... that's impractical.
	// Instead we test the safety property: creating two rooms must produce
	// distinct IDs and neither overwrites the other.
	room1, err := rc.CreateRoom(ctx, "Room One", "u1")
	require.NoError(t, err)
	room2, err := rc.CreateRoom(ctx, "Room Two", "u2")
	require.NoError(t, err)

	assert.NotEqual(t, room1.ID, room2.ID, "two rooms should have different IDs")

	// Both rooms should be intact.
	r1, err := rc.GetRoom(ctx, room1.ID)
	require.NoError(t, err)
	assert.Equal(t, "Room One", r1.Name)

	r2, err := rc.GetRoom(ctx, room2.ID)
	require.NoError(t, err)
	assert.Equal(t, "Room Two", r2.Name)

	// Both should appear in the rooms ZSet.
	_ = mr // keep mr reference
	ok1, _ := rc.IsRoomAccessible(ctx, room1.ID, "u1")
	ok2, _ := rc.IsRoomAccessible(ctx, room2.ID, "u2")
	assert.True(t, ok1)
	assert.True(t, ok2)
}

func TestReactionToggle_ReactedByMe(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()

	ms := time.Now().UnixMilli()
	msg := model.Message{
		ID:          fmt.Sprintf("%d-u", ms),
		RoomID:      "rm",
		UserID:      "u",
		Text:        "x",
		CreatedAt:   time.UnixMilli(ms),
		CreatedAtMS: fmt.Sprintf("%d", ms),
	}
	require.NoError(t, rc.SaveMessage(ctx, msg))

	_, err := rc.ToggleReaction(ctx, msg.ID, "❤️", "user1")
	require.NoError(t, err)

	reactions, err := rc.GetReactions(ctx, msg.ID, "user1")
	require.NoError(t, err)
	require.Len(t, reactions, 1)
	assert.True(t, reactions[0].ReactedByMe, "user1 should see ReactedByMe=true")

	reactions2, err := rc.GetReactions(ctx, msg.ID, "user2")
	require.NoError(t, err)
	require.Len(t, reactions2, 1)
	assert.False(t, reactions2[0].ReactedByMe, "user2 should see ReactedByMe=false")
}

func TestReactionOrdering(t *testing.T) {
	rc, _ := newClient(t)
	ctx := context.Background()

	ms := time.Now().UnixMilli()
	msg := model.Message{
		ID:          fmt.Sprintf("%d-order", ms),
		RoomID:      "rm",
		UserID:      "u",
		Text:        "order test",
		CreatedAt:   time.UnixMilli(ms),
		CreatedAtMS: fmt.Sprintf("%d", ms),
	}
	require.NoError(t, rc.SaveMessage(ctx, msg))

	// User1 adds A, B, C in order. Sleep 1ms between each to guarantee distinct timestamps.
	_, err := rc.ToggleReaction(ctx, msg.ID, "👍", "user1")
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	_, err = rc.ToggleReaction(ctx, msg.ID, "❤️", "user1")
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	_, err = rc.ToggleReaction(ctx, msg.ID, "🎉", "user1")
	require.NoError(t, err)

	// User2 also reacts with C (giving it the highest count) and adds D.
	_, err = rc.ToggleReaction(ctx, msg.ID, "🎉", "user2")
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	_, err = rc.ToggleReaction(ctx, msg.ID, "🚀", "user2")
	require.NoError(t, err)

	reactions, err := rc.GetReactions(ctx, msg.ID, "")
	require.NoError(t, err)
	require.Len(t, reactions, 4)

	emojis := make([]string, len(reactions))
	for i, r := range reactions {
		emojis[i] = r.Emoji
	}
	assert.Equal(t, []string{"👍", "❤️", "🎉", "🚀"}, emojis, "reactions should be in first-use order, not count order")

	t.Run("re-add after full removal gets fresh timestamp", func(t *testing.T) {
		// Remove all of 🎉 (user1 and user2 both un-react).
		_, err = rc.ToggleReaction(ctx, msg.ID, "🎉", "user1")
		require.NoError(t, err)
		_, err = rc.ToggleReaction(ctx, msg.ID, "🎉", "user2")
		require.NoError(t, err)

		// Re-add 🎉 — should now appear last (after 🚀); sleep to get a timestamp after 🚀.
		time.Sleep(time.Millisecond)
		_, err = rc.ToggleReaction(ctx, msg.ID, "🎉", "user1")
		require.NoError(t, err)

		reactions2, err := rc.GetReactions(ctx, msg.ID, "")
		require.NoError(t, err)
		require.Len(t, reactions2, 4)

		emojis2 := make([]string, len(reactions2))
		for i, r := range reactions2 {
			emojis2[i] = r.Emoji
		}
		assert.Equal(t, []string{"👍", "❤️", "🚀", "🎉"}, emojis2, "re-added emoji should appear after 🚀")
	})
}
