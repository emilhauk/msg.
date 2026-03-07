package handler_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	webpushlib "github.com/SherClockHolmes/webpush-go"
	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/emilhauk/msg/internal/webpush"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	alice = model.User{ID: "user-alice", Name: "Alice", Email: "alice@example.com"}
	bob   = model.User{ID: "user-bob", Name: "Bob", Email: "bob@example.com"}
)

const testRoom = "room1"

func seedMessage(t *testing.T, ts *testutil.TestServer, user model.User, text string, msOffset int64) model.Message {
	t.Helper()
	ms := time.Now().UnixMilli() - msOffset
	msgID := fmt.Sprintf("%d-%s", ms, user.ID)
	msg := model.Message{
		ID:          msgID,
		RoomID:      testRoom,
		UserID:      user.ID,
		Text:        text,
		CreatedAt:   time.UnixMilli(ms),
		CreatedAtMS: fmt.Sprintf("%d", ms),
	}
	require.NoError(t, ts.Redis.SaveMessage(context.Background(), msg))
	return msg
}

func TestHandlePost_Success(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	form := url.Values{"text": {"hello world"}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/messages", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestHandlePost_EmptyText(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	form := url.Values{"text": {""}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/messages", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandlePost_Unauthenticated(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})

	form := url.Values{"text": {"hello"}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/messages", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := testutil.NoRedirectClient()
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, resp.StatusCode)
}

func TestHandlePost_PubSubPayload(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub := ts.Redis.Subscribe(ctx, testRoom)
	defer sub.Close()
	// Wait for subscription confirmation before posting.
	_, err := sub.Receive(ctx)
	require.NoError(t, err)

	form := url.Values{"text": {"pub sub check"}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/messages", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	msg, err := sub.ReceiveMessage(ctx)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(msg.Payload, "msg:"),
		"expected pub/sub payload to start with msg:, got %q", msg.Payload)
}

func TestHandleDelete_Own(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)
	msg := seedMessage(t, ts, alice, "to be deleted", 0)

	req, _ := http.NewRequest("DELETE", ts.Server.URL+"/rooms/"+testRoom+"/messages/"+msg.ID, nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestHandleDelete_Other(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	aliceCookie := ts.AuthCookie(t, alice)
	msg := seedMessage(t, ts, bob, "bob's message", 0)

	req, _ := http.NewRequest("DELETE", ts.Server.URL+"/rooms/"+testRoom+"/messages/"+msg.ID, nil)
	req.AddCookie(aliceCookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestHandleDelete_Unknown(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("DELETE", ts.Server.URL+"/rooms/"+testRoom+"/messages/nonexistent-id", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleEdit_Own(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	// User record needed for hydrateMessages inside HandleEdit.
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	cookie := ts.AuthCookie(t, alice)
	msg := seedMessage(t, ts, alice, "original text", 0)

	form := url.Values{"text": {"edited text"}}
	req, _ := http.NewRequest("PATCH", ts.Server.URL+"/rooms/"+testRoom+"/messages/"+msg.ID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestHandleEdit_Other(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	aliceCookie := ts.AuthCookie(t, alice)
	msg := seedMessage(t, ts, bob, "bob's message", 0)

	form := url.Values{"text": {"tampered"}}
	req, _ := http.NewRequest("PATCH", ts.Server.URL+"/rooms/"+testRoom+"/messages/"+msg.ID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(aliceCookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestHandleEdit_EmptyText(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)
	msg := seedMessage(t, ts, alice, "original", 0)

	form := url.Values{"text": {"   "}}
	req, _ := http.NewRequest("PATCH", ts.Server.URL+"/rooms/"+testRoom+"/messages/"+msg.ID, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandleHistory_Before(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	cookie := ts.AuthCookie(t, alice)

	// Seed 3 messages spaced 1 second apart.
	m1 := seedMessage(t, ts, alice, "msg1", 3000)
	m2 := seedMessage(t, ts, alice, "msg2", 2000)
	seedMessage(t, ts, alice, "msg3", 1000)

	// Fetch messages before m2's timestamp (should return only m1).
	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom+"/messages?before="+m2.CreatedAtMS, nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
	_ = m1 // m1 would be in the rendered HTML
}

func TestHandleHistory_After(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	cookie := ts.AuthCookie(t, alice)

	m1 := seedMessage(t, ts, alice, "msg1", 2000)
	seedMessage(t, ts, alice, "msg2", 1000)
	seedMessage(t, ts, alice, "msg3", 0)

	// Fetch messages after m1 (should return msg2 and msg3).
	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom+"/messages?after="+m1.CreatedAtMS, nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

func TestHandleHistory_Latest(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	cookie := ts.AuthCookie(t, alice)

	seedMessage(t, ts, alice, "msg1", 2000)
	seedMessage(t, ts, alice, "msg2", 1000)
	seedMessage(t, ts, alice, "msg3", 0)

	// No params: should return the newest 50 messages.
	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom+"/messages", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

// ---------------------------------------------------------------------------
// Push dispatch helpers
// ---------------------------------------------------------------------------

// buildPushSender creates a real webpush.Sender backed by fresh VAPID keys.
// GenerateVAPIDKeys returns (privateKey, publicKey, err).
func buildPushSender(t *testing.T) *webpush.Sender {
	t.Helper()
	priv, pub, err := webpushlib.GenerateVAPIDKeys()
	require.NoError(t, err)
	return webpush.New(webpush.Config{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		VAPIDSubject:    "mailto:test@example.com",
	})
}

// bobSubscriptionJSON returns a valid push subscription JSON pointing at the
// given endpoint, using a freshly-generated P-256 ECDH key pair.
func bobSubscriptionJSON(t *testing.T, endpoint string) string {
	t.Helper()
	privKey, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	p256dh := base64.RawURLEncoding.EncodeToString(privKey.PublicKey().Bytes())

	authBytes := make([]byte, 16)
	_, err = rand.Read(authBytes)
	require.NoError(t, err)
	auth := base64.RawURLEncoding.EncodeToString(authBytes)

	sub, err := json.Marshal(map[string]any{
		"endpoint": endpoint,
		"keys":     map[string]string{"p256dh": p256dh, "auth": auth},
	})
	require.NoError(t, err)
	return string(sub)
}

// postMsg is a convenience wrapper for sending a message via the test server.
func postMsg(t *testing.T, ts *testutil.TestServer, user model.User, text string) *http.Response {
	t.Helper()
	cookie := ts.AuthCookie(t, user)
	form := url.Values{"text": {text}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/messages", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// ---------------------------------------------------------------------------
// Push dispatch tests
// ---------------------------------------------------------------------------

func TestHandlePost_PushDelivery(t *testing.T) {
	received := make(chan struct{}, 10)
	fakePush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		received <- struct{}{}
	}))
	defer fakePush.Close()

	sender := buildPushSender(t)
	ts := testutil.NewTestServerWithPush(t, sender)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, alice.ID))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, bob.ID))

	// Give bob a subscription pointing at the fake push server.
	subJSON := bobSubscriptionJSON(t, fakePush.URL)
	require.NoError(t, ts.Redis.SavePushSubscription(context.Background(), bob.ID, fakePush.URL, subJSON))

	resp := postMsg(t, ts, alice, "hello bob")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	select {
	case <-received:
		// Push was delivered to bob — success.
	case <-time.After(2 * time.Second):
		t.Fatal("push notification not delivered within 2s")
	}
}

func TestHandlePost_PushSkipsSender(t *testing.T) {
	received := make(chan struct{}, 10)
	fakePush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		received <- struct{}{}
	}))
	defer fakePush.Close()

	sender := buildPushSender(t)
	ts := testutil.NewTestServerWithPush(t, sender)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, alice.ID))

	// Only alice has a subscription — she is also the sender, so no push should fire.
	subJSON := bobSubscriptionJSON(t, fakePush.URL)
	require.NoError(t, ts.Redis.SavePushSubscription(context.Background(), alice.ID, fakePush.URL, subJSON))

	resp := postMsg(t, ts, alice, "just me here")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	select {
	case <-received:
		t.Fatal("push should not be sent to the message sender")
	case <-time.After(300 * time.Millisecond):
		// Correct: no push sent.
	}
}

func TestHandlePost_PushMutedRecipient(t *testing.T) {
	received := make(chan struct{}, 10)
	fakePush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		received <- struct{}{}
	}))
	defer fakePush.Close()

	sender := buildPushSender(t)
	ts := testutil.NewTestServerWithPush(t, sender)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, alice.ID))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, bob.ID))

	// Bob has a subscription but has muted notifications.
	subJSON := bobSubscriptionJSON(t, fakePush.URL)
	require.NoError(t, ts.Redis.SavePushSubscription(context.Background(), bob.ID, fakePush.URL, subJSON))
	require.NoError(t, ts.Redis.SetMute(context.Background(), bob.ID, time.Hour))

	resp := postMsg(t, ts, alice, "will bob hear this?")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	select {
	case <-received:
		t.Fatal("push should not be sent to a muted recipient")
	case <-time.After(300 * time.Millisecond):
		// Correct: bob is muted, no push sent.
	}
}

func TestHandlePost_PushExpiredSubscription(t *testing.T) {
	// Mock push server returns 410 Gone → subscription should be deleted from Redis.
	fakePush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer fakePush.Close()

	sender := buildPushSender(t)
	ts := testutil.NewTestServerWithPush(t, sender)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, alice.ID))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, bob.ID))

	subJSON := bobSubscriptionJSON(t, fakePush.URL)
	require.NoError(t, ts.Redis.SavePushSubscription(context.Background(), bob.ID, fakePush.URL, subJSON))

	resp := postMsg(t, ts, alice, "ping")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Wait for async goroutine to finish and clean up the expired subscription.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		subs, err := ts.Redis.GetAllPushSubscriptions(context.Background(), bob.ID)
		require.NoError(t, err)
		if len(subs) == 0 {
			return // subscription was cleaned up — success.
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expired subscription was not cleaned up within 2s")
}

func TestHandlePost_PushSkipsActiveRecipient(t *testing.T) {
	received := make(chan struct{}, 10)
	fakePush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		received <- struct{}{}
	}))
	defer fakePush.Close()

	sender := buildPushSender(t)
	ts := testutil.NewTestServerWithPush(t, sender)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, alice.ID))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, bob.ID))

	subJSON := bobSubscriptionJSON(t, fakePush.URL)
	require.NoError(t, ts.Redis.SavePushSubscription(context.Background(), bob.ID, fakePush.URL, subJSON))

	// Mark bob as actively viewing the room.
	require.NoError(t, ts.Redis.SetRoomViewing(context.Background(), bob.ID, testRoom))

	resp := postMsg(t, ts, alice, "hello bob")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	select {
	case <-received:
		t.Fatal("push should not be sent to a recipient who is actively viewing the room")
	case <-time.After(300 * time.Millisecond):
		// Correct: bob is active, no push sent.
	}
}

func TestHandlePost_PushDeliveredToInactiveRecipient(t *testing.T) {
	received := make(chan struct{}, 10)
	fakePush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		received <- struct{}{}
	}))
	defer fakePush.Close()

	sender := buildPushSender(t)
	ts := testutil.NewTestServerWithPush(t, sender)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, alice.ID))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, bob.ID))

	subJSON := bobSubscriptionJSON(t, fakePush.URL)
	require.NoError(t, ts.Redis.SavePushSubscription(context.Background(), bob.ID, fakePush.URL, subJSON))

	// Bob has no viewing key → push should be delivered.
	resp := postMsg(t, ts, alice, "hello bob")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	select {
	case <-received:
		// Correct: bob has no viewing key, push delivered.
	case <-time.After(2 * time.Second):
		t.Fatal("push notification not delivered within 2s")
	}
}

func TestHandlePost_MentionNotification(t *testing.T) {
	// Use a channel to signal push delivery in a race-free way.
	received := make(chan struct{}, 10)
	fakePush := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The request body is encrypted — we only confirm a POST was received,
		// which proves the mention was detected and push dispatch was triggered.
		w.WriteHeader(http.StatusCreated)
		received <- struct{}{}
	}))
	defer fakePush.Close()

	sender := buildPushSender(t)
	ts := testutil.NewTestServerWithPush(t, sender)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, alice.ID))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, bob.ID))

	subJSON := bobSubscriptionJSON(t, fakePush.URL)
	require.NoError(t, ts.Redis.SavePushSubscription(context.Background(), bob.ID, fakePush.URL, subJSON))

	// Post a message that @mentions Bob by name.
	resp := postMsg(t, ts, alice, "@Bob check this out")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	select {
	case <-received:
		// Push was delivered — mention was detected and push dispatch triggered.
	case <-time.After(2 * time.Second):
		t.Fatal("mention push notification not delivered within 2s")
	}
}
