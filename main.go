package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"text/template"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/emilhauk/msg/internal/auth"
	"github.com/emilhauk/msg/internal/handler"
	"github.com/emilhauk/msg/internal/middleware"
	"github.com/emilhauk/msg/internal/model"
	redisclient "github.com/emilhauk/msg/internal/redis"
	"github.com/emilhauk/msg/internal/storage"
	"github.com/emilhauk/msg/internal/tmpl"
	"github.com/emilhauk/msg/internal/webpush"
)

// buildVersion is the short git SHA injected at build time via:
//
//	go build -ldflags "-X main.buildVersion=$(git rev-parse --short HEAD)"
//
// Falls back to "dev" for local builds without the flag.
var buildVersion = "dev"

//go:embed web
var webFS embed.FS

func main() {
	logLevel, err := zerolog.ParseLevel(envOrDefault("LOG_LEVEL", "info"))
	if err != nil {
		logLevel = zerolog.InfoLevel
	}
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	zerolog.SetGlobalLevel(logLevel)

	redisURL := envOrDefault("REDIS_URL", "redis://localhost:6379")
	baseURL := envOrDefault("BASE_URL", "http://localhost:8080")
	port := envOrDefault("PORT", "8080")
	openRegistration := strings.EqualFold(envOrDefault("OPEN_REGISTRATION", "false"), "true")
	allowList := parseAllowList(envOrDefault("ALLOW_LIST", ""))
	joinApprovers := parseAllowList(envOrDefault("JOIN_APPROVER", ""))
	enablePasswordLogin := strings.EqualFold(envOrDefault("ENABLE_PASSWORD_LOGIN", "false"), "true")

	sessionSecret, err := resolveSessionSecret()
	if err != nil {
		log.Fatal().Err(err).Msg("resolve session secret")
	}

	redis, err := redisclient.New(redisURL)
	if err != nil {
		log.Fatal().Err(err).Msg("connect to redis")
	}
	defer redis.Close()

	// Seed default room.
	if err := redis.SeedRoom(context.Background(), model.Room{ID: "bemro", Name: "Project BEMRØ"}); err != nil {
		log.Fatal().Err(err).Msg("seed room")
	}

	// Ensure all existing users have a name_index entry for uniqueness checks.
	if err := redis.SeedNameIndexes(context.Background()); err != nil {
		log.Warn().Err(err).Msg("seed name indexes")
	}

	// Migrate legacy String identity keys to Hash format.
	if err := redis.MigrateIdentityKeys(context.Background()); err != nil {
		log.Warn().Err(err).Msg("migrate identity keys")
	}

	// Templates.
	webSubFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal().Err(err).Msg("sub fs")
	}
	renderer, err := tmpl.New(webSubFS)
	if err != nil {
		log.Fatal().Err(err).Msg("parse templates")
	}
	renderer.BuildVersion = buildVersion

	// S3 / media storage (optional — upload routes are only registered when configured).
	var s3Client *storage.S3Client
	if ep := envOrDefault("S3_ENDPOINT", ""); ep != "" {
		s3Client, err = storage.NewS3Client(storage.Config{
			Endpoint:        ep,
			Bucket:          envOrDefault("S3_BUCKET", ""),
			Region:          envOrDefault("S3_REGION", "us-east-1"),
			AccessKeyID:     envOrDefault("S3_ACCESS_KEY_ID", ""),
			SecretAccessKey: envOrDefault("S3_SECRET_ACCESS_KEY", ""),
		})
		if err != nil {
			log.Fatal().Err(err).Msg("connect to S3")
		}
	}

	// VAPID / Web Push (optional — disabled when VAPID_PUBLIC_KEY is unset).
	vapidCfg := webpush.Config{
		VAPIDPublicKey:  envOrDefault("VAPID_PUBLIC_KEY", ""),
		VAPIDPrivateKey: envOrDefault("VAPID_PRIVATE_KEY", ""),
		VAPIDSubject:    envOrDefault("VAPID_SUBJECT", ""),
	}
	var pushSender *webpush.Sender
	if vapidCfg.IsConfigured() {
		pushSender = webpush.New(vapidCfg)
		log.Info().Msg("web push: enabled")
	} else {
		log.Info().Msg("web push: disabled (VAPID_PUBLIC_KEY / VAPID_PRIVATE_KEY / VAPID_SUBJECT not set)")
	}

	// Handlers.
	authHandler := &auth.Handler{
		Redis:              redis,
		SessionSecret:      sessionSecret,
		BaseURL:            baseURL,
		OpenRegistration:   openRegistration,
		AllowList:          allowList,
		GitHubClientID:     envOrDefault("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret: envOrDefault("GITHUB_CLIENT_SECRET", ""),
		GoogleClientID:     envOrDefault("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: envOrDefault("GOOGLE_CLIENT_SECRET", ""),
	}
	passwordHandler := &auth.PasswordHandler{
		Redis:            redis,
		SessionSecret:    sessionSecret,
		BaseURL:          baseURL,
		OpenRegistration: openRegistration,
		AllowList:        allowList,
	}
	if enablePasswordLogin {
		log.Info().Msg("password auth: enabled")
	} else {
		log.Info().Msg("password auth: disabled (ENABLE_PASSWORD_LOGIN not set)")
	}
	roomsHandler := &handler.RoomsHandler{
		Redis:         redis,
		Renderer:      renderer,
		BaseURL:       baseURL,
		JoinApprovers: joinApprovers,
	}
	messagesHandler := &handler.MessagesHandler{
		Redis:    redis,
		Renderer: renderer,
		S3:       s3Client,
		Push:     pushSender,
		BaseURL:  baseURL,
	}
	reactionsHandler := &handler.ReactionsHandler{Redis: redis, Renderer: renderer}
	sseHandler := &handler.SSEHandler{Redis: redis, Version: buildVersion}
	notificationsHandler := &handler.NotificationsHandler{
		Redis:          redis,
		Push:           pushSender,
		VAPIDPublicKey: vapidCfg.VAPIDPublicKey,
	}

	profileHandler := &handler.ProfileHandler{
		Redis:         redis,
		Renderer:      renderer,
		SessionSecret: sessionSecret,
		Secure:        strings.HasPrefix(baseURL, "https://"),
		GitHubEnabled: os.Getenv("GITHUB_CLIENT_ID") != "",
		GoogleEnabled: os.Getenv("GOOGLE_CLIENT_ID") != "",
	}

	secure := strings.HasPrefix(baseURL, "https://")
	authMW := middleware.RequireAuth(redis, sessionSecret, secure)

	mux := http.NewServeMux()

	// Chroma syntax-highlight CSS (generated at startup, served dynamically).
	chromaCSS := buildChromaCSS()
	mux.HandleFunc("GET /static/chroma.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write([]byte(chromaCSS))
	})

	// Web App Manifest — no-cache; icon URLs are cache-busted via buildVersion query param.
	manifestTmpl := `{"name":"msg.","short_name":"msg.","description":"A fast, real-time chat for your team","start_url":"/","scope":"/","display":"standalone","background_color":"#1a1d23","theme_color":"#5865f2","icons":[{"src":"/static/logo_192.png?v={{.V}}","sizes":"192x192","type":"image/png"},{"src":"/static/logo_512.png?v={{.V}}","sizes":"512x512","type":"image/png"}]}`
	manifestT := template.Must(template.New("manifest").Parse(manifestTmpl))
	mux.HandleFunc("GET /manifest.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_ = manifestT.Execute(w, map[string]string{"V": buildVersion})
	})

	// Service Worker — served at root scope with no-cache so the browser always
	// checks for updates. Must NOT be under /static/ (scope would be too narrow).
	swBytes, swErr := fs.ReadFile(webSubFS, "static/sw.js")
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		if swErr != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Service-Worker-Allowed", "/")
		_, _ = w.Write(swBytes)
	})

	// Static assets — served with a long-lived immutable cache header.
	// The ?v=<buildVersion> query string in templates busts the cache on deploy.
	staticHandler := http.FileServerFS(webSubFS)
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		staticHandler.ServeHTTP(w, r)
	}))

	// Health check — no auth required; must be reachable before a session exists.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Auth routes (no auth required).
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		var errMsg string
		switch r.URL.Query().Get("error") {
		case "access_denied":
			errMsg = "You are not on the access list. Contact the administrator to request access."
		case "invalid_credentials":
			errMsg = "Invalid email or password."
		}
		renderer.Render(w, http.StatusOK, "login.html", map[string]any{
			"ErrorMsg":            errMsg,
			"PasswordAuthEnabled": enablePasswordLogin,
			"GitHubAuthEnabled":   authHandler.GitHubClientID != "",
			"GoogleAuthEnabled":   authHandler.GoogleClientID != "",
		})
	})
	mux.HandleFunc("GET /auth/{provider}", authHandler.HandleLogin)
	mux.HandleFunc("GET /auth/{provider}/callback", authHandler.HandleCallback)
	mux.HandleFunc("POST /auth/logout", authHandler.HandleLogout)

	// Password auth route — only registered when the feature is enabled.
	if enablePasswordLogin {
		mux.HandleFunc("POST /auth/password/login", passwordHandler.HandleLogin)
	}

	// Protected routes.
	mux.Handle("GET /", authMW(http.HandlerFunc(roomsHandler.HandleRoot)))
	mux.Handle("POST /rooms", authMW(http.HandlerFunc(roomsHandler.HandleCreate)))
	mux.Handle("GET /rooms/unread-counts", authMW(http.HandlerFunc(roomsHandler.HandleUnreadCounts)))
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

	// User profile routes.
	mux.Handle("GET /user/profile", authMW(http.HandlerFunc(profileHandler.HandleProfile)))
	mux.Handle("PATCH /user/profile", authMW(http.HandlerFunc(profileHandler.HandleUpdateName)))
	mux.Handle("PATCH /user/avatar", authMW(http.HandlerFunc(profileHandler.HandleUpdateAvatar)))
	mux.Handle("POST /user/profile/delete", authMW(http.HandlerFunc(profileHandler.HandleDelete)))
	mux.Handle("POST /user/identities/{provider}/disconnect", authMW(http.HandlerFunc(profileHandler.HandleDisconnect)))

	// Push notification routes.
	mux.HandleFunc("GET /push/vapid-public-key", notificationsHandler.HandleVAPIDPublicKey)
	mux.Handle("POST /push/subscribe", authMW(http.HandlerFunc(notificationsHandler.HandleSubscribe)))
	mux.Handle("DELETE /push/subscribe", authMW(http.HandlerFunc(notificationsHandler.HandleUnsubscribe)))
	mux.Handle("GET /settings/mute", authMW(http.HandlerFunc(notificationsHandler.HandleGetMute)))
	mux.Handle("POST /settings/mute", authMW(http.HandlerFunc(notificationsHandler.HandleSetMute)))
	mux.Handle("DELETE /settings/mute", authMW(http.HandlerFunc(notificationsHandler.HandleClearMute)))

	// Avatar proxy — unauthenticated; serves profile images with a stable URL and cache headers.
	mux.Handle("GET /avatar/{userID}", &handler.AvatarHandler{Redis: redis})

	// Media upload — only available when S3 is configured.
	if s3Client != nil {
		uploadHandler := &handler.UploadHandler{Redis: redis, S3: s3Client}
		mux.Handle("GET /rooms/{id}/upload-url", authMW(http.HandlerFunc(uploadHandler.HandlePresignURL)))
	}

	addr := ":" + port
	log.Info().Str("addr", addr).Msg("listening")
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal().Err(err).Msg("server")
	}
}

// buildChromaCSS generates the CSS for syntax highlighting in both light
// ("github") and dark ("github-dark") themes, wrapped in appropriate
// @media / [data-theme] selectors so it integrates with the existing theme
// toggle system.
func buildChromaCSS() string {
	light, err := tmpl.ChromaCSS("github")
	if err != nil {
		log.Error().Err(err).Msg("chroma: generate light CSS")
	}
	dark, err := tmpl.ChromaCSS("github-dark")
	if err != nil {
		log.Error().Err(err).Msg("chroma: generate dark CSS")
	}

	return light + "\n" +
		"@media (prefers-color-scheme: dark) {\n  :root[data-theme=\"auto\"] {\n" + dark + "\n  }\n}\n" +
		":root[data-theme=\"dark\"] {\n" + dark + "\n}\n"
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// resolveSessionSecret reads SESSION_SECRET from the environment and decodes it
// as a hex string. If the variable is not set, a random 32-byte secret is
// generated and an error is logged to warn that all sessions will be
// invalidated on the next restart.
func resolveSessionSecret() ([]byte, error) {
	raw := os.Getenv("SESSION_SECRET")
	if raw == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		log.Error().Msg("SESSION_SECRET is not set — using a random secret; all sessions will be invalidated on restart")
		return secret, nil
	}
	secret, err := hex.DecodeString(raw)
	if err != nil || len(secret) == 0 {
		return nil, fmt.Errorf("SESSION_SECRET must be a non-empty hex string")
	}
	return secret, nil
}

// parseAllowList splits a comma-separated list of emails, lowercases and trims
// each entry, and discards empty strings.
func parseAllowList(raw string) []string {
	var list []string
	for _, entry := range strings.Split(raw, ",") {
		if e := strings.ToLower(strings.TrimSpace(entry)); e != "" {
			list = append(list, e)
		}
	}
	return list
}
