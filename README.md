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

OAuth callback URLs to register with each provider:

```
http://localhost:8080/auth/github/callback
http://localhost:8080/auth/google/callback
http://localhost:8080/auth/discord/callback
```

### 3. Run

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
