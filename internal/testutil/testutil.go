// Package testutil provides shared helpers for HTTP integration tests.
// All test packages are two levels deep under internal/, so the web templates
// are always reachable at "../../web" relative to the CWD set by `go test`.
package testutil

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/emilhauk/msg/internal/auth"
	"github.com/emilhauk/msg/internal/handler"
	"github.com/emilhauk/msg/internal/middleware"
	"github.com/emilhauk/msg/internal/model"
	redisclient "github.com/emilhauk/msg/internal/redis"
	"github.com/emilhauk/msg/internal/tmpl"
	"github.com/emilhauk/msg/internal/webpush"
)

// testSecret is a fixed 32-byte signing secret used across all tests.
var testSecret = func() []byte {
	s := make([]byte, 32)
	for i := range s {
		s[i] = byte(i + 1)
	}
	return s
}()

// TestServer wraps an httptest.Server with direct access to the underlying
// miniredis instance and redis client for seeding and asserting state.
type TestServer struct {
	Server *httptest.Server
	Redis  *redisclient.Client
	MR     *miniredis.Miniredis
	Secret []byte
}

// NewTestServer creates a fully wired HTTP test server backed by an in-process
// miniredis. All resources are automatically cleaned up via t.Cleanup.
func NewTestServer(t *testing.T) *TestServer {
	return newTestServer(t, nil)
}

// NewTestServerWithPush creates a test server with a configured Web Push sender
// wired into MessagesHandler so that push dispatch tests can exercise the full flow.
func NewTestServerWithPush(t *testing.T, push *webpush.Sender) *TestServer {
	return newTestServer(t, push)
}

func newTestServer(t *testing.T, push *webpush.Sender) *TestServer {
	t.Helper()

	mr := miniredis.RunT(t)

	rc, err := redisclient.New("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("testutil: connect to miniredis: %v", err)
	}
	t.Cleanup(func() { rc.Close() })

	// Templates are loaded from the "web/" directory. All test packages sit
	// two levels below the module root (internal/<pkg>/), so ../../web is always
	// the correct relative path regardless of which package's tests are running.
	webFS := os.DirFS("../../web")
	renderer, err := tmpl.New(webFS)
	if err != nil {
		t.Fatalf("testutil: parse templates: %v", err)
	}

	mux := buildMux(rc, renderer, webFS, testSecret, push)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &TestServer{
		Server: srv,
		Redis:  rc,
		MR:     mr,
		Secret: testSecret,
	}
}

// AuthCookie creates a session for user directly in miniredis and returns a
// signed cookie ready to attach to test requests. This avoids the login flow.
func (ts *TestServer) AuthCookie(t *testing.T, user model.User) *http.Cookie {
	t.Helper()
	signed, err := auth.SignToken(ts.Secret)
	if err != nil {
		t.Fatalf("testutil: sign token: %v", err)
	}
	token, err := auth.VerifyToken(ts.Secret, signed)
	if err != nil {
		t.Fatalf("testutil: verify token: %v", err)
	}
	if err := ts.Redis.SetSession(context.Background(), token, user); err != nil {
		t.Fatalf("testutil: set session: %v", err)
	}
	return &http.Cookie{Name: "session", Value: signed}
}

// SeedRoom inserts a room record into miniredis.
func (ts *TestServer) SeedRoom(t *testing.T, room model.Room) {
	t.Helper()
	if err := ts.Redis.SeedRoom(context.Background(), room); err != nil {
		t.Fatalf("testutil: seed room %q: %v", room.ID, err)
	}
}

// GrantAccess adds userID to roomID's access list.
func (ts *TestServer) GrantAccess(t *testing.T, roomID, userID string) {
	t.Helper()
	if err := ts.Redis.AddRoomAccess(context.Background(), roomID, userID); err != nil {
		t.Fatalf("testutil: grant access %q→%q: %v", userID, roomID, err)
	}
}

// buildMux mirrors the route wiring in main.go with password auth always
// enabled and optional integrations (S3) omitted.
// push may be nil — when non-nil it is wired into MessagesHandler for push dispatch tests.
func buildMux(rc *redisclient.Client, renderer *tmpl.Renderer, webFS fs.FS, secret []byte, push *webpush.Sender) http.Handler {
	passwordHandler := &auth.PasswordHandler{
		Redis:            rc,
		SessionSecret:    secret,
		OpenRegistration: true, // no allow-list by default; tests can use a restricted server
	}
	roomsHandler := &handler.RoomsHandler{
		Redis:         rc,
		Renderer:      renderer,
		BaseURL:       "http://localhost",
		JoinApprovers: []string{"approver@example.com"},
	}
	messagesHandler := &handler.MessagesHandler{Redis: rc, Renderer: renderer, Push: push}
	reactionsHandler := &handler.ReactionsHandler{Redis: rc, Renderer: renderer}
	sseHandler := &handler.SSEHandler{Redis: rc, Version: "test"}
	notificationsHandler := &handler.NotificationsHandler{
		Redis:          rc,
		VAPIDPublicKey: "test-vapid-public-key",
	}

	authMW := middleware.RequireAuth(rc, secret, false)
	mux := http.NewServeMux()

	// Static assets — needed for browser tests (SW registration, etc.).
	staticFS, err := fs.Sub(webFS, "static")
	if err == nil {
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
		mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
			data, ferr := fs.ReadFile(staticFS, "sw.js")
			if ferr != nil {
				http.Error(w, "sw.js not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Service-Worker-Allowed", "/")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(data) //nolint:errcheck
		})
	}

	mux.HandleFunc("POST /auth/password/login", passwordHandler.HandleLogin)

	mux.Handle("GET /", authMW(http.HandlerFunc(roomsHandler.HandleRoot)))
	mux.Handle("POST /rooms", authMW(http.HandlerFunc(roomsHandler.HandleCreate)))
	mux.Handle("GET /rooms/{id}", authMW(http.HandlerFunc(roomsHandler.HandleRoom)))
	mux.Handle("GET /rooms/{id}/panel", authMW(http.HandlerFunc(roomsHandler.HandlePanel)))
	mux.Handle("POST /rooms/{id}/access", authMW(http.HandlerFunc(roomsHandler.HandleAddAccess)))
	mux.Handle("POST /rooms/{id}/invites", authMW(http.HandlerFunc(roomsHandler.HandleCreateInvite)))
	mux.Handle("GET /join/{token}", authMW(http.HandlerFunc(roomsHandler.HandleJoin)))
	mux.Handle("DELETE /rooms/{id}/leave", authMW(http.HandlerFunc(roomsHandler.HandleLeave)))
	mux.Handle("POST /rooms/{id}/messages", authMW(http.HandlerFunc(messagesHandler.HandlePost)))
	mux.Handle("GET /rooms/{id}/messages", authMW(http.HandlerFunc(messagesHandler.HandleHistory)))
	mux.Handle("GET /rooms/{id}/events", authMW(http.HandlerFunc(sseHandler.HandleSSE)))
	mux.Handle("DELETE /rooms/{id}/messages/{msgID}", authMW(http.HandlerFunc(messagesHandler.HandleDelete)))
	mux.Handle("PATCH /rooms/{id}/messages/{msgID}", authMW(http.HandlerFunc(messagesHandler.HandleEdit)))
	mux.Handle("POST /rooms/{id}/messages/{msgID}/reactions", authMW(http.HandlerFunc(reactionsHandler.HandleToggle)))
	mux.Handle("GET /rooms/{id}/members", authMW(http.HandlerFunc(notificationsHandler.HandleRoomMembers)))
	mux.Handle("POST /rooms/{id}/active", authMW(http.HandlerFunc(notificationsHandler.HandleRoomActive)))
	mux.Handle("POST /rooms/{id}/inactive", authMW(http.HandlerFunc(notificationsHandler.HandleRoomInactive)))
	mux.Handle("GET /user/events", authMW(http.HandlerFunc(sseHandler.HandleUserSSE)))

	// Push notification routes.
	mux.HandleFunc("GET /push/vapid-public-key", notificationsHandler.HandleVAPIDPublicKey)
	mux.Handle("POST /push/subscribe", authMW(http.HandlerFunc(notificationsHandler.HandleSubscribe)))
	mux.Handle("DELETE /push/subscribe", authMW(http.HandlerFunc(notificationsHandler.HandleUnsubscribe)))
	mux.Handle("GET /settings/mute", authMW(http.HandlerFunc(notificationsHandler.HandleGetMute)))
	mux.Handle("POST /settings/mute", authMW(http.HandlerFunc(notificationsHandler.HandleSetMute)))
	mux.Handle("DELETE /settings/mute", authMW(http.HandlerFunc(notificationsHandler.HandleClearMute)))

	return mux
}

// NoRedirectClient returns an *http.Client that does not follow redirects.
// Use this when testing login flows where you need to inspect the 302 Location header.
func NoRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
