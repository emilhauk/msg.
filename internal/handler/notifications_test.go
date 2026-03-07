package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleVAPIDPublicKey(t *testing.T) {
	ts := testutil.NewTestServer(t)

	resp, err := http.Get(ts.Server.URL + "/push/vapid-public-key")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "test-vapid-public-key", body["key"])
}

func TestHandleSubscribe(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	body := `{"endpoint":"https://push.example.com/sub/1","keys":{"p256dh":"abc","auth":"def"}}`
	req, _ := http.NewRequest("POST", ts.Server.URL+"/push/subscribe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Subscription should appear in Redis.
	subs, err := ts.Redis.GetAllPushSubscriptions(context.Background(), alice.ID)
	require.NoError(t, err)
	assert.Len(t, subs, 1)
}

func TestHandleSubscribe_InvalidBody(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("POST", ts.Server.URL+"/push/subscribe", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandleUnsubscribe(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)
	endpoint := "https://push.example.com/sub/delete-me"

	// Seed a subscription directly in Redis.
	require.NoError(t, ts.Redis.SavePushSubscription(context.Background(), alice.ID, endpoint,
		`{"endpoint":"`+endpoint+`","keys":{"p256dh":"x","auth":"y"}}`))

	body := `{"endpoint":"` + endpoint + `"}`
	req, _ := http.NewRequest("DELETE", ts.Server.URL+"/push/subscribe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Subscription should be gone.
	subs, err := ts.Redis.GetAllPushSubscriptions(context.Background(), alice.ID)
	require.NoError(t, err)
	assert.Empty(t, subs)
}

func TestHandleSetMute_Valid(t *testing.T) {
	durations := []string{"1h", "8h", "24h", "168h", "forever"}
	for _, d := range durations {
		t.Run(d, func(t *testing.T) {
			ts := testutil.NewTestServer(t)
			cookie := ts.AuthCookie(t, alice)

			body := `{"duration":"` + d + `"}`
			req, _ := http.NewRequest("POST", ts.Server.URL+"/settings/mute", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(cookie)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusNoContent, resp.StatusCode)

			muted, err := ts.Redis.IsMuted(context.Background(), alice.ID)
			require.NoError(t, err)
			assert.True(t, muted, "should be muted after setting duration %q", d)
		})
	}
}

func TestHandleSetMute_Invalid(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	body := `{"duration":"invalid-value"}`
	req, _ := http.NewRequest("POST", ts.Server.URL+"/settings/mute", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandleClearMute(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	// Seed a mute first.
	require.NoError(t, ts.Redis.SetMute(context.Background(), alice.ID, time.Hour))

	req, _ := http.NewRequest("DELETE", ts.Server.URL+"/settings/mute", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	muted, err := ts.Redis.IsMuted(context.Background(), alice.ID)
	require.NoError(t, err)
	assert.False(t, muted)
}

func TestHandleGetMute_NotMuted(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/settings/mute", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, false, body["muted"])
	assert.Nil(t, body["until"])
}

func TestHandleGetMute_Timed(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	require.NoError(t, ts.Redis.SetMute(context.Background(), alice.ID, time.Hour))

	req, _ := http.NewRequest("GET", ts.Server.URL+"/settings/mute", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, true, body["muted"])
	untilStr, ok := body["until"].(string)
	require.True(t, ok, "until should be a string")
	// Should be a valid RFC3339 timestamp.
	_, err = time.Parse(time.RFC3339, untilStr)
	assert.NoError(t, err, "until should be RFC3339: %s", untilStr)
}

func TestHandleGetMute_Forever(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	require.NoError(t, ts.Redis.SetMute(context.Background(), alice.ID, 0))

	req, _ := http.NewRequest("GET", ts.Server.URL+"/settings/mute", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, true, body["muted"])
	assert.Equal(t, "forever", body["until"])
}

func TestHandleRoomActive(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/active", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	viewing, err := ts.Redis.IsRoomViewing(context.Background(), alice.ID, testRoom)
	require.NoError(t, err)
	assert.True(t, viewing, "viewing key should be set after /active")
}

func TestHandleRoomInactive(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	// Seed a viewing key first.
	require.NoError(t, ts.Redis.SetRoomViewing(context.Background(), alice.ID, testRoom))

	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/inactive", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	viewing, err := ts.Redis.IsRoomViewing(context.Background(), alice.ID, testRoom)
	require.NoError(t, err)
	assert.False(t, viewing, "viewing key should be cleared after /inactive")
}

func TestHandleRoomMembers(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))

	// Touch room members so they appear in the members list.
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, alice.ID))
	require.NoError(t, ts.Redis.TouchRoomMember(context.Background(), testRoom, bob.ID))

	cookie := ts.AuthCookie(t, alice)
	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom+"/members", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	var members []map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&members))
	assert.Len(t, members, 2)

	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, m["name"])
	}
	assert.ElementsMatch(t, []string{"Alice", "Bob"}, names)
}
