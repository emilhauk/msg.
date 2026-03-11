package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// approver is a user whose email matches the testutil JoinApprovers list.
var approver = model.User{ID: "user-approver", Name: "Approver", Email: "approver@example.com"}

func TestHandleRoom_Success(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom, nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

func TestHandleRoom_Unauthenticated(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})

	client := testutil.NoRedirectClient()
	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom, nil)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Location"), "/login")
}

func TestHandleRoom_NotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/does-not-exist", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleRoom_Forbidden(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	// alice has no access granted → 403
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom, nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Room creation
// ---------------------------------------------------------------------------

func TestHandleCreate_Success(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	client := testutil.NoRedirectClient()
	form := url.Values{"name": {"My New Room"}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	loc := resp.Header.Get("Location")
	require.True(t, strings.HasPrefix(loc, "/rooms/"), "expected redirect to new room, got %q", loc)

	// Extract roomID from Location and verify the room exists with access for alice.
	roomID := strings.TrimPrefix(loc, "/rooms/")
	ok, err := ts.Redis.IsRoomAccessible(context.Background(), roomID, alice.ID)
	require.NoError(t, err)
	assert.True(t, ok, "creator should have access to the new room")
}

func TestHandleCreate_EmptyName(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	form := url.Values{"name": {"   "}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Room panel
// ---------------------------------------------------------------------------

func TestHandlePanel_Success(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom+"/panel", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "Alice", "panel should list the member's name")
}

func TestHandlePanel_ShowsMembers(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, testRoom, alice.ID)
	ts.GrantAccess(t, testRoom, bob.ID)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom+"/panel", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	assert.Contains(t, html, "Alice")
	assert.Contains(t, html, "Bob")
}

func TestHandlePanel_CanInviteExternal_Approver(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), approver))
	ts.GrantAccess(t, testRoom, approver.ID)
	cookie := ts.AuthCookie(t, approver)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom+"/panel", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "Generate invite link",
		"approver should see the invite link section")
}

func TestHandlePanel_CannotInviteExternal_NonApprover(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom+"/panel", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.NotContains(t, string(body), "Generate invite link",
		"non-approver should not see the invite link section")
}

func TestHandlePanel_Forbidden(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	cookie := ts.AuthCookie(t, alice) // no access granted

	req, _ := http.NewRequest("GET", ts.Server.URL+"/rooms/"+testRoom+"/panel", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Add access
// ---------------------------------------------------------------------------

func TestHandleAddAccess_ValidCandidate(t *testing.T) {
	ts := testutil.NewTestServer(t)
	// Two rooms: room1 and room2.
	ts.SeedRoom(t, model.Room{ID: "room1", Name: "Room 1"})
	ts.SeedRoom(t, model.Room{ID: "room2", Name: "Room 2"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	// Alice and Bob both have access to room1.
	ts.GrantAccess(t, "room1", alice.ID)
	ts.GrantAccess(t, "room1", bob.ID)
	// Alice has access to room2, but Bob does not.
	ts.GrantAccess(t, "room2", alice.ID)
	cookie := ts.AuthCookie(t, alice)

	// Alice adds Bob to room2 — Bob is a valid candidate because they share room1.
	client := testutil.NoRedirectClient()
	form := url.Values{"user_id": {bob.ID}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/room2/access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)

	ok, err := ts.Redis.IsRoomAccessible(context.Background(), "room2", bob.ID)
	require.NoError(t, err)
	assert.True(t, ok, "bob should have access to room2 after being invited")
}

func TestHandleAddAccess_InvalidCandidate(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	charlie := model.User{ID: "user-charlie", Name: "Charlie", Email: "charlie@example.com"}
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), charlie))
	// Alice has access to the room. Charlie exists but shares NO rooms with Alice.
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	form := url.Values{"user_id": {charlie.ID}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Verify Charlie does NOT have access.
	ok, err := ts.Redis.IsRoomAccessible(context.Background(), testRoom, charlie.ID)
	require.NoError(t, err)
	assert.False(t, ok, "charlie should not have access — not a valid candidate")
}

func TestHandleAddAccess_UserNotFound(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	form := url.Values{"user_id": {"nonexistent-user-id"}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestHandleAddAccess_AlreadyMember(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: "room1", Name: "Room 1"})
	ts.SeedRoom(t, model.Room{ID: "room2", Name: "Room 2"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	// Alice and Bob share room1 (so Bob is a valid candidate).
	ts.GrantAccess(t, "room1", alice.ID)
	ts.GrantAccess(t, "room1", bob.ID)
	// Both already have access to room2.
	ts.GrantAccess(t, "room2", alice.ID)
	ts.GrantAccess(t, "room2", bob.ID)
	cookie := ts.AuthCookie(t, alice)

	// Alice tries to add Bob to room2 — Bob already has access → 409.
	client := testutil.NoRedirectClient()
	form := url.Values{"user_id": {bob.ID}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/room2/access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestHandleAddAccess_Forbidden(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	// alice has no access to the room → cannot invite
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	cookie := ts.AuthCookie(t, alice)

	form := url.Values{"user_id": {bob.ID}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestHandleAddAccess_BroadcastsMemberStatus(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: "room1", Name: "Room 1"})
	ts.SeedRoom(t, model.Room{ID: "room2", Name: "Room 2"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, "room1", alice.ID)
	ts.GrantAccess(t, "room1", bob.ID)
	ts.GrantAccess(t, "room2", alice.ID)
	cookie := ts.AuthCookie(t, alice)

	sub := ts.Redis.Subscribe(context.Background(), "room2")
	defer sub.Close()
	ch := sub.Channel()

	client := testutil.NoRedirectClient()
	form := url.Values{"user_id": {bob.ID}}
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/room2/access", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)

	select {
	case msg := <-ch:
		assert.Contains(t, msg.Payload, "memberstatus:")
		assert.Contains(t, msg.Payload, bob.ID)
		assert.Contains(t, msg.Payload, `"isMember":true`)
	case <-time.After(2 * time.Second):
		t.Fatal("expected memberstatus broadcast when adding member")
	}
}

func TestHandleLeave_BroadcastsMemberStatus(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	require.NoError(t, ts.Redis.CreateUser(context.Background(), bob))
	ts.GrantAccess(t, testRoom, alice.ID)
	ts.GrantAccess(t, testRoom, bob.ID)
	cookie := ts.AuthCookie(t, alice)

	sub := ts.Redis.Subscribe(context.Background(), testRoom)
	defer sub.Close()
	ch := sub.Channel()

	client := testutil.NoRedirectClient()
	req, _ := http.NewRequest("DELETE", ts.Server.URL+"/rooms/"+testRoom+"/leave", nil)
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)

	select {
	case msg := <-ch:
		assert.Contains(t, msg.Payload, "memberstatus:")
		assert.Contains(t, msg.Payload, alice.ID)
		assert.Contains(t, msg.Payload, `"isMember":false`)
	case <-time.After(2 * time.Second):
		t.Fatal("expected memberstatus broadcast when leaving room")
	}
}

func TestHandleJoin_BroadcastsMemberStatus(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, approver.ID)
	token, err := ts.Redis.CreateInviteToken(context.Background(), testRoom, approver.ID)
	require.NoError(t, err)

	sub := ts.Redis.Subscribe(context.Background(), testRoom)
	defer sub.Close()
	ch := sub.Channel()

	cookie := ts.AuthCookie(t, bob)
	client := testutil.NoRedirectClient()
	req, _ := http.NewRequest("GET", ts.Server.URL+"/join/"+token, nil)
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)

	select {
	case msg := <-ch:
		assert.Contains(t, msg.Payload, "memberstatus:")
		assert.Contains(t, msg.Payload, bob.ID)
		assert.Contains(t, msg.Payload, `"isMember":true`)
	case <-time.After(2 * time.Second):
		t.Fatal("expected memberstatus broadcast when joining via invite")
	}
}

// ---------------------------------------------------------------------------
// Invite links
// ---------------------------------------------------------------------------

func TestHandleCreateInvite_Approver(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), approver))
	ts.GrantAccess(t, testRoom, approver.ID)
	cookie := ts.AuthCookie(t, approver)

	client := testutil.NoRedirectClient()
	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/invites", nil)
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	// The redirect Location should contain the token.
	loc := resp.Header.Get("Location")
	assert.Contains(t, loc, "invite_url=", "redirect should include token")
}

func TestHandleCreateInvite_NonApprover(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	require.NoError(t, ts.Redis.CreateUser(context.Background(), alice))
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("POST", ts.Server.URL+"/rooms/"+testRoom+"/invites", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestHandleJoin_ValidToken(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, approver.ID)
	// Create a token directly in Redis.
	token, err := ts.Redis.CreateInviteToken(context.Background(), testRoom, approver.ID)
	require.NoError(t, err)

	// Bob (not yet a member) uses the invite link.
	cookie := ts.AuthCookie(t, bob)
	client := testutil.NoRedirectClient()
	req, _ := http.NewRequest("GET", ts.Server.URL+"/join/"+token, nil)
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/rooms/"+testRoom, resp.Header.Get("Location"))

	// Bob should now have access.
	ok, err := ts.Redis.IsRoomAccessible(context.Background(), testRoom, bob.ID)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestHandleJoin_TokenSingleUse(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	token, err := ts.Redis.CreateInviteToken(context.Background(), testRoom, alice.ID)
	require.NoError(t, err)

	// First use: bob joins.
	cookieBob := ts.AuthCookie(t, bob)
	client := testutil.NoRedirectClient()
	req1, _ := http.NewRequest("GET", ts.Server.URL+"/join/"+token, nil)
	req1.AddCookie(cookieBob)
	resp1, err := client.Do(req1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSeeOther, resp1.StatusCode)

	// Second use: should be 404 (token consumed).
	req2, _ := http.NewRequest("GET", ts.Server.URL+"/join/"+token, nil)
	req2.AddCookie(cookieBob)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestHandleJoin_ExpiredToken(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/join/nonexistenttoken", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// ---------------------------------------------------------------------------
// Root redirect
// ---------------------------------------------------------------------------

func TestHandleRoot_RedirectsToFirstRoom(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	client := testutil.NoRedirectClient()
	req, _ := http.NewRequest("GET", ts.Server.URL+"/", nil)
	req.AddCookie(cookie)

	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Location"), "/rooms/")
}

func TestHandleRoot_NoRooms(t *testing.T) {
	ts := testutil.NewTestServer(t)
	cookie := ts.AuthCookie(t, alice)

	req, _ := http.NewRequest("GET", ts.Server.URL+"/", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "Create room")
}
