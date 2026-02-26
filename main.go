package main

import (
	"context"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/emilhauk/chat/internal/auth"
	"github.com/emilhauk/chat/internal/handler"
	"github.com/emilhauk/chat/internal/middleware"
	"github.com/emilhauk/chat/internal/model"
	redisclient "github.com/emilhauk/chat/internal/redis"
	"github.com/emilhauk/chat/internal/storage"
	"github.com/emilhauk/chat/internal/tmpl"
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
	redisURL := envOrDefault("REDIS_URL", "redis://localhost:6379")
	sessionSecretHex := envOrDefault("SESSION_SECRET", "0000000000000000000000000000000000000000000000000000000000000000")
	baseURL := envOrDefault("BASE_URL", "http://localhost:8080")
	port := envOrDefault("PORT", "8080")
	openRegistration := strings.EqualFold(envOrDefault("OPEN_REGISTRATION", "false"), "true")
	allowList := parseAllowList(envOrDefault("ALLOW_LIST", ""))

	sessionSecret, err := hex.DecodeString(sessionSecretHex)
	if err != nil || len(sessionSecret) == 0 {
		log.Fatalf("invalid SESSION_SECRET: must be a hex string")
	}

	redis, err := redisclient.New(redisURL)
	if err != nil {
		log.Fatalf("connect to redis: %v", err)
	}
	defer redis.Close()

	// Seed default room.
	if err := redis.SeedRoom(context.Background(), model.Room{ID: "bemro", Name: "Project BEMRØ"}); err != nil {
		log.Fatalf("seed room: %v", err)
	}

	// Templates.
	webSubFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("sub fs: %v", err)
	}
	renderer, err := tmpl.New(webSubFS)
	if err != nil {
		log.Fatalf("parse templates: %v", err)
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
			log.Fatalf("connect to S3: %v", err)
		}
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
	}
	roomsHandler := &handler.RoomsHandler{Redis: redis, Renderer: renderer}
	messagesHandler := &handler.MessagesHandler{Redis: redis, Renderer: renderer, S3: s3Client}
	reactionsHandler := &handler.ReactionsHandler{Redis: redis, Renderer: renderer}
	sseHandler := &handler.SSEHandler{Redis: redis, Version: buildVersion}

	authMW := middleware.RequireAuth(redis, sessionSecret)

	mux := http.NewServeMux()

	// Chroma syntax-highlight CSS (generated at startup, served dynamically).
	chromaCSS := buildChromaCSS()
	mux.HandleFunc("GET /static/chroma.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write([]byte(chromaCSS))
	})

	// Static assets — served with a long-lived immutable cache header.
	// The ?v=<buildVersion> query string in templates busts the cache on deploy.
	staticHandler := http.FileServerFS(webSubFS)
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		staticHandler.ServeHTTP(w, r)
	}))

	// Auth routes (no auth required).
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		var errMsg string
		if r.URL.Query().Get("error") == "access_denied" {
			errMsg = "You are not on the access list. Contact the administrator to request access."
		}
		renderer.Render(w, http.StatusOK, "login.html", map[string]any{"ErrorMsg": errMsg})
	})
	mux.HandleFunc("GET /auth/{provider}", authHandler.HandleLogin)
	mux.HandleFunc("GET /auth/{provider}/callback", authHandler.HandleCallback)
	mux.HandleFunc("POST /auth/logout", authHandler.HandleLogout)

	// Protected routes.
	mux.Handle("GET /", authMW(http.HandlerFunc(roomsHandler.HandleRoot)))
	mux.Handle("GET /rooms/{id}", authMW(http.HandlerFunc(roomsHandler.HandleRoom)))
	mux.Handle("POST /rooms/{id}/messages", authMW(http.HandlerFunc(messagesHandler.HandlePost)))
	mux.Handle("GET /rooms/{id}/messages", authMW(http.HandlerFunc(messagesHandler.HandleHistory)))
	mux.Handle("GET /rooms/{id}/events", authMW(http.HandlerFunc(sseHandler.HandleSSE)))
	mux.Handle("DELETE /rooms/{id}/messages/{msgID}", authMW(http.HandlerFunc(messagesHandler.HandleDelete)))
	mux.Handle("POST /rooms/{id}/messages/{msgID}/reactions", authMW(http.HandlerFunc(reactionsHandler.HandleToggle)))

	// Media upload — only available when S3 is configured.
	if s3Client != nil {
		uploadHandler := &handler.UploadHandler{S3: s3Client}
		mux.Handle("GET /rooms/{id}/upload-url", authMW(http.HandlerFunc(uploadHandler.HandlePresignURL)))
	}

	addr := ":" + port
	fmt.Printf("listening on %s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// buildChromaCSS generates the CSS for syntax highlighting in both light
// ("github") and dark ("github-dark") themes, wrapped in appropriate
// @media / [data-theme] selectors so it integrates with the existing theme
// toggle system.
func buildChromaCSS() string {
	light, err := tmpl.ChromaCSS("github")
	if err != nil {
		log.Printf("chroma: generate light CSS: %v", err)
	}
	dark, err := tmpl.ChromaCSS("github-dark")
	if err != nil {
		log.Printf("chroma: generate dark CSS: %v", err)
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
