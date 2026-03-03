# msg.

A minimal public chat-room application. Real-time messages delivered via Server-Sent Events, OAuth login (GitHub, Google, Discord), and link previews — no JavaScript framework required.

## Stack

- **Go** — HTTP server, business logic
- **HTMX** — reactive UI without a JS framework
- **Redis** — sole data store (messages, sessions, pub/sub)
- **SSE** — real-time message fan-out

## Running locally

### Prerequisites

- Go 1.25+
- Docker (for Redis)
- OAuth credentials for at least one provider (GitHub, Google, or Discord)

### 1. Start Redis

```sh
docker run -d -p 6379:6379 redis:alpine
```

Or use the included Compose file, which also spins up RedisInsight:

```sh
docker compose up redis -d
```

### 2. Configure environment

```sh
cp .env.example .env
```

Fill in `.env`:

| Variable | Description |
|---|---|
| `REDIS_URL` | Redis connection URL, e.g. `redis://localhost:6379` |
| `SESSION_SECRET` | Random 32-byte hex string — `openssl rand -hex 32` |
| `BASE_URL` | Public-facing base URL, e.g. `http://localhost:8080` |
| `PORT` | HTTP port (default `8080`) |
| `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` | [GitHub OAuth app](https://github.com/settings/developers) |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | [Google OAuth credentials](https://console.cloud.google.com/apis/credentials) |
| `DISCORD_CLIENT_ID` / `DISCORD_CLIENT_SECRET` | [Discord application](https://discord.com/developers/applications) |
| `OPEN_REGISTRATION` | `true` = anyone may log in; `false` (default) = allowlist only |
| `ALLOW_LIST` | Comma-separated emails allowed when `OPEN_REGISTRATION=false` |
| `MICROLINK_API_KEY` | Optional — only needed above the free tier |
| `S3_ENDPOINT` | S3-compatible endpoint, e.g. `https://s3.example.com` — omit to disable media uploads |
| `S3_BUCKET` | Bucket name for uploaded media |
| `S3_REGION` | Region string (MinIO accepts any value, e.g. `us-east-1`) |
| `S3_ACCESS_KEY_ID` | S3 access key ID |
| `S3_SECRET_ACCESS_KEY` | S3 secret access key |

OAuth callback URLs to register with each provider:

```
http://localhost:8080/auth/github/callback
http://localhost:8080/auth/google/callback
http://localhost:8080/auth/discord/callback
```

### 3. Configure media uploads (optional)

Media uploads (paste to send images/video) require an S3-compatible object store such as [MinIO](https://min.io/). The steps below use [`mc`](https://min.io/docs/minio/linux/reference/minio-mc.html) (the MinIO CLI client) and assume:

- MinIO is reachable at `https://s3.example.com`
- The app is at `https://msg.example.com`

**Install `mc`:**

```sh
# macOS
brew install minio/stable/mc

# Linux
curl https://dl.min.io/client/mc/release/linux-amd64/mc \
  -o /usr/local/bin/mc && chmod +x /usr/local/bin/mc
```

> On Arch Linux the package is `mcli` (naming conflict with Midnight Commander). Set `MC_CONFIG_DIR="${XDG_CONFIG_HOME}/mc"` to keep config out of `~/.mc`.

**Register your MinIO instance as an alias:**

```sh
mc alias set myminio https://s3.example.com ACCESS_KEY_ID SECRET_ACCESS_KEY
```

**Create the bucket and make it publicly readable:**

```sh
mc mb myminio/msg-media
mc anonymous set download myminio/msg-media
```

**Apply the CORS policy** 

For MinIO; this can be done by setting the env var:
```yaml
service:
  storage:
    environment:
      MINIO_API_CORS_ALLOWED_ORIGINS: "http://localhost:8080"
```

**Add to `.env`:**

```sh
S3_ENDPOINT=https://s3.example.com
S3_BUCKET=msg-media
S3_REGION=us-east-1
S3_ACCESS_KEY_ID=your_access_key_id
S3_SECRET_ACCESS_KEY=your_secret_access_key
```

`S3_REGION` can be any non-empty string; MinIO ignores it. If `S3_ENDPOINT` is not set the upload route is not registered and the paste-to-upload handler silently does nothing.

### 4. Run

```sh
export $(grep -v '^#' .env | xargs)
go run ./...
```

Open [http://localhost:8080](http://localhost:8080).

### Live reload (optional)

The project ships with an [Air](https://github.com/air-verse/air) config for hot-reload during development:

```sh
go install github.com/air-verse/air@latest
air -c .air.toml
```

Or start everything via Docker Compose (uses Air inside the container):

```sh
docker compose up
```

## Testing

### Unit and integration tests

No external services required — tests use an in-process Redis (miniredis).

```sh
go test ./... -race -timeout 60s -count=1 -short
```

### JS linting

```sh
npm run lint
```

### E2E browser tests

Requires headless Chromium (go-rod will find it automatically if installed).

```sh
go test ./internal/browser/... -v -timeout 120s
```

To run with a **visible browser window** — useful for debugging or watching tests execute:

```sh
HEADLESS=false go test ./internal/browser/... -v -timeout 120s
```

To run a single test:

```sh
HEADLESS=false go test ./internal/browser/... -v -timeout 120s -run TestThemeToggle_DarkOS
```

### Run everything

```sh
make test
```

This runs lint, unit/integration tests, and E2E browser tests in sequence.

## Container image

Production images are published to the GitHub Container Registry on every push:

```
ghcr.io/emilhauk/msg:<branch>
ghcr.io/emilhauk/msg:<short-sha>
```

Pull and run:

```sh
docker run -p 8080:8080 --env-file .env \
  -e REDIS_URL=redis://your-redis:6379 \
  ghcr.io/emilhauk/msg:main
```

## Project structure

```
.
├── main.go                  # Entry point: routes, server startup, room seeding
├── internal/
│   ├── auth/                # OAuth flow and signed-cookie sessions
│   ├── handler/             # HTTP handlers (rooms, messages, SSE)
│   ├── middleware/          # Session validation
│   ├── model/               # Shared structs
│   ├── redis/               # Typed Redis helpers
│   └── tmpl/                # Template rendering
└── web/
    ├── templates/           # HTML templates (base layout, room, message partials)
    └── static/              # CSS
```

## License

[MIT](LICENSE)
