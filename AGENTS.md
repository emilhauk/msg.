# Chat — Project Guide

## Working Convention

AGENTS.md is the agent's persistent memory. Update it proactively whenever:
- A technical decision is made or changed
- A non-obvious pattern or constraint is discovered
- A feature is added, changed, or removed
- Anything would otherwise need re-discovery next session

No permission needed — treat it as a working document, not documentation.

---

A simple public chat-room web app built with Go and HTMX. Real-time via Server-Sent Events. Redis is the sole data store. One active room ("Project BEMRØ", id `bemro`); the data model supports multiple rooms from day one.

---

## Tech Stack

| Layer | Choice | Notes |
|---|---|---|
| Language | Go (latest stable) | Standard library preferred; add dependencies deliberately |
| Frontend | HTMX | No JS framework; vanilla JS only where HTMX cannot reach |
| Styling | Plain CSS | No Tailwind or CSS frameworks |
| Real-time | Server-Sent Events | One `/events` SSE endpoint per room |
| Storage | Redis | All state lives here; no SQL |
| Auth | OAuth 2.0 | **GitHub only** (Google/Discord not implemented yet) |
| Sessions | Signed cookies | HMAC-SHA256 token; 90-day TTL refreshed on each request |
| Emoji picker | emoji-picker-element | jsDelivr CDN; cached in IndexedDB |
| Link previews | Microlink API | Async, server-side; Redis-cached |
| Media uploads | S3-compatible (MinIO) | Presigned PUT; optional (disabled when `S3_ENDPOINT` unset) |
| Syntax highlighting | Chroma | Server-side; CSS generated at startup, served at `/static/chroma.css` |

---

## Implemented Features (as of latest commit)

- GitHub OAuth login with allow-list or open registration
- Single chat room with real-time SSE messaging
- Message delete (author only; SSE-broadcast to all clients)
- Emoji reactions (toggle; preset popover + full picker; SSE-broadcast)
- Emoji shortcode autocomplete (`:thumbs` → dropdown in textarea)
- Link preview unfurls via Microlink (async; Redis-cached)
- Media attachments: paste, drag-drop, file picker → presigned S3 PUT
- Fenced code blocks with syntax highlighting and copy button
- Infinite scroll (sentinel div, `hx-trigger="revealed"`)
- Auto-scroll to bottom; pauses when user has scrolled up
- Auto-reload on deploy (SSE `version` event; respects focus state)
- Light/dark/auto theme toggle (persisted in localStorage)
- Cache-busted static assets (`?v=<gitSHA>`)

---

## Project Layout

```
main.go                        # Entry point: wire routes, seed room, serve
internal/
  auth/
    oauth.go                   # OAuth flow; GitHub only; HandleLogin, HandleCallback, HandleLogout
    session.go                 # HMAC cookie sign/verify; SignToken, VerifyToken, SetCookie, ClearCookie
  handler/
    rooms.go                   # GET / → redirect /rooms/bemro; GET /rooms/{id}
    messages.go                # POST /rooms/{id}/messages (204); GET /rooms/{id}/messages (history)
                               #   DELETE /rooms/{id}/messages/{msgID} (author-only)
                               #   hydrateMessages() — user+unfurl+reactions per message
    reactions.go               # POST /rooms/{id}/messages/{msgID}/reactions — toggle, SSE broadcast
    upload.go                  # GET /rooms/{id}/upload-url — presigned S3 PUT (optional)
    sse.go                     # GET /rooms/{id}/events — Redis Pub/Sub → SSE fan-out
    unfurl.go                  # fetchMicrolink(); isValidHTTPURL()
  middleware/
    auth.go                    # RequireAuth middleware; UserFromContext()
  model/
    types.go                   # User, Room, Message, Attachment, Reaction, Unfurl, MessageView
  redis/
    client.go                  # All Redis operations (typed helpers only — no raw commands in handlers)
  storage/
    s3.go                      # S3Client: PresignPut, PublicURL, KeyFromURL, DeleteObjects, MediaKey
  tmpl/
    render.go                  # Renderer; funcMap (renderText, linkify, reactionData, …); ChromaCSS
web/
  templates/
    base.html                  # Layout: HTMX, SSE ext, emoji-picker-element; theme/emoji/copy JS
    room.html                  # Room page; SSE wiring; all vanilla JS (scroll, upload, reactions, autocomplete)
    message.html               # Message partial (used for SSE push and initial render)
    reactions.html             # Reactions bar partial (used standalone for SSE reaction events)
    history.html               # Infinite-scroll history partial (sentinel + messages)
    unfurl.html                # Link preview card partial
    login.html                 # Login page (GitHub button)
    error.html                 # Error page
  static/
    style.css                  # All styles
    favicon.svg
    logo_square_256.png
compose.yml                    # Docker Compose: app + Redis + RedisInsight + MinIO
Dockerfile / Dockerfile.prod   # Dev and production builds
.air.toml                      # Air live-reload config (dev)
```

---

## Routes

```
GET  /login                                — login page
GET  /auth/{provider}                      — start OAuth (GitHub only)
GET  /auth/{provider}/callback             — OAuth callback
POST /auth/logout                          — clear session

GET  /                                     — redirect → /rooms/bemro
GET  /rooms/{id}                           — room page (last 50 msgs)
GET  /rooms/{id}/events                    — SSE stream (Redis Pub/Sub)
POST /rooms/{id}/messages                  — post message → 204 (SSE delivers to all)
GET  /rooms/{id}/messages?before=<ms>&limit=50  — paginated history partial
DELETE /rooms/{id}/messages/{msgID}        — delete own message → 204 + SSE delete event
POST /rooms/{id}/messages/{msgID}/reactions — toggle emoji reaction → 204 + SSE reaction event
GET  /rooms/{id}/upload-url?hash=&content_type=&content_length=  — presign S3 PUT (optional)

GET  /static/*                             — embedded static files (immutable cache)
GET  /static/chroma.css                    — generated syntax-highlight CSS
```

---

## Redis Key Schema

```
sessions:{token}              Hash    id, name, avatar_url, provider; TTL 90 days
oauth:state:{state}           String  CSRF state; TTL 10 min (consumed on use)
users:{id}                    Hash    id, name, avatar_url, provider (no TTL)
rooms                         ZSet    room IDs scored by creation time (unix seconds)
rooms:{id}                    Hash    id, name
rooms:{id}:messages           ZSet    message IDs scored by created_at (unix ms); cleaned on write
rooms:{id}:events             Pub/Sub SSE fan-out channel
messages:{msg-id}             Hash    id, room_id, user_id, text, attachments (JSON), created_at (ms); TTL 30 days
reactions:{msg-id}            Hash    emoji → count; TTL 30 days
reactions:{msg-id}:users      Hash    "{emoji}\x00{userID}" → "1"; TTL 30 days
unfurls:{sha256-of-url}       String  JSON Unfurl or "null"; TTL 24h (success) / 15 min (failure)
```

---

## SSE Payload Protocol

All payloads published to `rooms:{id}:events` use a prefix to identify type:

```
"msg:<html>"             → event: message   (HTMX sse-swap into #sse-message-target)
"unfurl:<msgId>:<html>"  → event: unfurl    (JS: innerHTML on #preview-<msgId>)
"reaction:<json>"        → event: reaction  (JS: JSON { msgId, reactorId, reactedEmojis, html })
"delete:<msgId>"         → event: delete    (JS: remove #msg-<msgId>)
```

On connect the server also sends:
```
event: version\ndata: <buildSHA>
```
Used by the client to detect deploys and trigger a reload.

---

## Two SSE Connections Per Client

The room page opens **two** `EventSource` connections to the same `/rooms/{id}/events` endpoint:

1. **HTMX-managed** (`sse-connect` on `<section>`): handles `event: message` only via `sse-swap`.
2. **Vanilla JS-managed** (in `room.html` `<script>`): handles `unfurl`, `reaction`, `delete`, and `version`.

Rationale: HTMX's SSE extension silently drops event types it is not `sse-swap`-ing. A dedicated native `EventSource` handles all non-message events cleanly.

---

## Message ID Format

```
{unixMillis}-{userID}
```
e.g. `1712345678901-github:12345678`. Both uniquely identifies the message and encodes the creation timestamp.

---

## Media Upload Flow

1. Client generates a 12-char random hex hash.
2. `GET /rooms/{id}/upload-url?hash=<hash>&content_type=<type>&content_length=<bytes>` → `{ upload_url, public_url, key }`.
3. Client PUTs directly to S3 using `upload_url`.
4. Client includes `{ url: public_url, content_type, filename: hash }` as JSON in the `attachments` form field on message POST.
5. Server validates content type and URL on POST; deletes S3 objects when message is deleted.

Allowed content types: `image/jpeg`, `image/png`, `image/gif`, `image/webp`, `video/mp4`, `video/webm`. Max size: **50 MiB**.

S3 key format: `rooms/{roomID}/{unixMs}-{userID}/{hash}.{ext}`

---

## Reaction Broadcast Strategy

Reactions are broadcast as **neutral HTML** (no `ReactedByMe` baked in for any user) plus the reacting user's emoji list. Each client applies its own active state from a local `__myReactions` map in JS, then calls `htmx.process()` on the swapped element so HTMX re-processes new `hx-post` attributes.

---

## Key Conventions

### Go
- Handlers are thin; business logic lives in `internal/` packages.
- `POST /rooms/{id}/messages` always returns `204 No Content`. Never return rendered HTML from POST — the message is delivered exclusively via SSE to avoid duplicates.
- All Redis operations go through typed helpers in `internal/redis/client.go`. Never scatter raw Redis commands in handlers.
- `context.Context` is threaded through all Redis and HTTP calls for proper cancellation.
- `middleware.UserFromContext(ctx)` retrieves the authenticated user; always available in protected routes.
- `model.MessageView` wraps `*model.Message` + `CurrentUserID` for templates that conditionally render owner-only controls.
- The `Renderer.RenderString()` method renders partials (SSE fragments) without the base layout and without wrapping in `pageData`.
- `Renderer.Render()` wraps data in `pageData{BuildVersion, Data}` — templates access `.Data.*` and `.BuildVersion`.

### CDN Policy
jsDelivr only. No other CDN without deliberate decision.

```
https://cdn.jsdelivr.net/npm/htmx.org@2/dist/htmx.min.js
https://cdn.jsdelivr.net/npm/htmx-ext-sse@2/sse.js
https://cdn.jsdelivr.net/npm/emoji-picker-element/+esm
```

### No build step
No webpack, vite, or any frontend bundler. No TypeScript compilation. No Tailwind. Static files are embedded via `//go:embed web`.

---

## Environment Variables

```
REDIS_URL              redis://localhost:6379
SESSION_SECRET         64-char hex string (32 bytes)
BASE_URL               public-facing URL, e.g. http://localhost:8080
PORT                   default 8080
OPEN_REGISTRATION      "true" = anyone may log in; "false" (default) = ALLOW_LIST only
ALLOW_LIST             comma-separated emails permitted to log in

GITHUB_CLIENT_ID
GITHUB_CLIENT_SECRET

# Google and Discord OAuth vars are documented but NOT yet implemented.

MICROLINK_API_KEY      optional; only needed above Microlink free-tier limits

S3_ENDPOINT            omit to disable media uploads (e.g. https://s3.example.com)
S3_BUCKET
S3_REGION              MinIO accepts any string (e.g. us-east-1)
S3_ACCESS_KEY_ID
S3_SECRET_ACCESS_KEY
```

---

## Decisions & Constraints

- **GitHub OAuth only.** `GOOGLE_CLIENT_ID` / `DISCORD_CLIENT_ID` are documented in the original spec but the handler rejects non-GitHub providers. Don't add them without a deliberate decision.
- **No room-creation UI.** Rooms are seeded at startup only (`SeedRoom` in `main.go`). The `bemro` room is the only active room.
- **No ORM.** Redis commands only, through `internal/redis/client.go`.
- **No SQL.** Redis is the only data store.
- **No client-side routing.** Every navigation is a full or partial (HTMX) page load.
- **Sessions TTL: 90 days** (cookie + Redis TTL are kept in sync; refreshed on every authenticated request).
- **Message TTL: 30 days.** Both the Hash and the sorted-set entry are cleaned up.
- **Syntax highlighting** uses Chroma (server-side). CSS is generated at startup for `github` (light) and `github-dark` (dark) themes and served dynamically from `/static/chroma.css`. The CSS integrates with the `[data-theme]` attribute system.
- **Emoji shortcode autocomplete** uses `emoji-picker-element`'s `Database` class exposed as `window.__EmojiDatabase` from the module script in `base.html`. The room page polls for it with `setInterval`.

---

## What to Avoid

- Do not return HTML from `POST /rooms/{id}/messages`. SSE delivers to everyone.
- Do not add raw Redis commands outside `internal/redis/client.go`.
- Do not add a SQL database or ORM.
- Do not add a frontend build step.
- Do not load assets from CDNs other than jsDelivr.
- Do not add room-creation UI or a multi-room sidebar (sidebar code is commented out in `room.html`).
- Do not add Google or Discord OAuth without a deliberate decision.

---

## Running Locally

```sh
# All services (app + Redis + RedisInsight + MinIO)
docker compose up

# Or just Redis, then run Go directly with air (live reload):
docker compose up redis
air
```

Build version is injected at build time:
```sh
go build -ldflags "-X main.buildVersion=$(git rev-parse --short HEAD)" .
```

---

## Definition of Done (per feature)

- Renders correctly with HTMX; no full-page reloads where a partial is expected.
- `POST` actions return `204`; all state changes delivered via SSE.
- No secrets committed; all config via env vars.
- Redis keys follow the schema above.
- New env vars documented in this file.
- New templates registered in `tmpl.New()` in `internal/tmpl/render.go`.
