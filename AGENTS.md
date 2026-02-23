# Chat — Project Guide

A simple public chat-room web application built with Go and HTMX. Real-time updates are delivered via Server-Sent Events (SSE). Redis is the sole data store. Currently a single room ("Project BEMRØ") is active; the data model is designed for multiple rooms from day one.

---

## Tech Stack

| Layer | Choice | Notes |
|---|---|---|
| Language | Go (latest stable) | Standard library preferred; add dependencies deliberately |
| Frontend | HTMX | No JS framework; sprinkle vanilla JS only where HTMX cannot reach |
| Styling | Plain CSS | No Tailwind or CSS frameworks unless explicitly decided |
| Real-time | Server-Sent Events | One `/events` SSE endpoint per room; HTMX `hx-ext="sse"` on the client |
| Storage | Redis | All state lives here; no SQL database |
| Auth | OAuth 2.0 | Providers: GitHub, Google, Discord |
| Sessions | Signed cookies | Store session token in a cookie; resolve user from Redis |
| Emoji picker | emoji-picker-element | Via jsDelivr CDN (`<script type="module">`); `emoji-click` appends unicode to textarea |
| Link previews | Microlink API | Async, server-side; result cached in Redis; included in history if cached |

---

## Architecture Overview

```
Browser
  └─ HTMX (SSE extension for live updates, hx-post/hx-get for actions)
       │
       ▼
Go HTTP Server
  ├─ OAuth handlers      /auth/{provider}, /auth/{provider}/callback
  ├─ Session middleware  validates cookie → resolves user
  ├─ Room handler        GET / → redirect to /rooms/bemro
  │                      GET /rooms/{id}
  ├─ Message handlers    POST /rooms/{id}/messages
  │                      GET  /rooms/{id}/messages?before=<timestamp-ms>&limit=50
  └─ SSE endpoint        GET /rooms/{id}/events
       │
       ▼
Redis
  ├─ sessions:{token}          Hash    – user info, expiry
  ├─ users:{id}                Hash    – id, name, avatar_url, provider
  ├─ rooms                     ZSet    – room IDs sorted by creation time
  ├─ rooms:{id}                Hash    – id, name
  ├─ rooms:{id}:messages       ZSet    – message IDs scored by Unix timestamp (ms)
  ├─ messages:{msg-id}         Hash    – id, room_id, user_id, text, created_at; TTL 30 days
  ├─ rooms:{id}:events         Pub/Sub – new message / unfurl notifications
  └─ unfurls:{url-sha256}      String  – Microlink JSON result; TTL 24h (success), 15 min (failure)
```

---

## Project Layout

```
.
├── AGENTS.md
├── go.mod
├── go.sum
├── main.go                  # Entry point: wire up routes, start server, seed rooms
├── internal/
│   ├── auth/
│   │   ├── oauth.go         # OAuth flow, provider config
│   │   └── session.go       # Cookie signing, session read/write
│   ├── handler/
│   │   ├── rooms.go         # GET / → redirect to /rooms/bemro; GET /rooms/{id}
│   │   ├── messages.go      # POST /rooms/{id}/messages; GET /rooms/{id}/messages (paginated history)
│   │   └── sse.go           # SSE endpoint, fan-out via Redis Pub/Sub
│   ├── middleware/
│   │   └── auth.go          # Session validation middleware
│   ├── model/
│   │   └── types.go         # User, Room, Message structs
│   ├── redis/
│   │   └── client.go        # Redis connection and typed helpers
│   └── tmpl/
│       └── render.go        # Template rendering helpers
└── web/
    ├── templates/
    │   ├── base.html        # Base layout with HTMX + SSE extension scripts
    │   ├── room.html        # Single room view (message list + sentinel div + input)
    │   ├── message.html     # Partial: one message row (returned by POST and SSE push)
    │   ├── unfurl.html      # Partial: link preview card (title, description, image)
    │   └── login.html       # Login / OAuth entry page
    └── static/
        └── style.css
```

---

## Key Conventions

### Go
- Keep handlers thin; business logic in `internal/` packages.
- `POST /rooms/{id}/messages` returns `204 No Content`; do **not** return a rendered partial. The message is delivered to all clients (including the sender) exclusively via SSE. This prevents the sender seeing duplicates.
- Use `context.Context` for cancellation (especially in the SSE handler).
- Wrap all Redis calls in typed helpers (`internal/redis`); never use raw string commands scattered through handlers.
- Errors bubble up to a central handler that renders a minimal error partial.
- Configuration via environment variables only (no config files). Required vars:

  ```
  REDIS_URL              e.g. redis://localhost:6379
  SESSION_SECRET         random 32-byte hex string
  BASE_URL               public-facing URL e.g. http://localhost:8080
  GITHUB_CLIENT_ID
  GITHUB_CLIENT_SECRET
  GOOGLE_CLIENT_ID
  GOOGLE_CLIENT_SECRET
  DISCORD_CLIENT_ID
  DISCORD_CLIENT_SECRET
  MICROLINK_API_KEY      optional; only required if exceeding the free tier
  PORT                   default 8080
  OPEN_REGISTRATION      "true" = anyone may log in; "false" (default) = only emails in ALLOW_LIST are permitted
  ALLOW_LIST             comma-separated list of email addresses allowed to log in, e.g. alice@example.com,bob@example.com
  ```

### CDN Policy
jsDelivr (`https://cdn.jsdelivr.net/`) is the preferred CDN for all external assets. No other CDN should be introduced without a deliberate decision. Assets loaded from jsDelivr:

```
https://cdn.jsdelivr.net/npm/htmx.org@2/dist/htmx.min.js
https://cdn.jsdelivr.net/npm/htmx-ext-sse@2/sse.js
https://cdn.jsdelivr.net/npm/emoji-picker-element/+esm
```

### HTMX
- Room page receives new messages via SSE (`hx-ext="sse"`, `sse-connect`, `sse-swap`).
- Message form uses `hx-post` with `hx-swap="none"`; the server returns `204 No Content`. All clients (including the sender) receive the message exclusively via SSE, avoiding duplicates.
- Do not use `hx-push-url` on SSE-only updates.

#### SSE — two connections per client
The room page opens **two** `EventSource` connections to `/rooms/{id}/events`:

1. **HTMX-managed** (`sse-connect` on the `<section>`): listens for `event: message`, swaps rendered message HTML into the message list via `sse-swap`.
2. **Vanilla JS-managed** (opened in a `<script>` block in `room.html`): listens exclusively for `event: unfurl`. When received, parses the data as `<msgId>:<html>` and sets `innerHTML` on `#preview-<msgId>`.

Rationale: HTMX's SSE extension only dispatches `htmx:sseMessage` for events it is actively handling via `sse-swap`. Custom event types (like per-message unfurl notifications) are silently dropped by HTMX. A dedicated native `EventSource` avoids any reliance on HTMX internals and keeps the two concerns cleanly separated.

#### Emoji Picker
- `<emoji-picker>` is placed in `base.html` **outside** any HTMX-swappable region so it survives DOM swaps.
- A button next to the message input toggles its visibility via a small vanilla JS snippet.
- A single `emoji-click` event listener appends `event.detail.unicode` to the message `<textarea>`.
- Emoji data is fetched from jsDelivr on first load and cached in IndexedDB; subsequent loads have zero network cost for emoji data.

#### Infinite Scroll (message history)
- On room visit, the server renders the last 50 messages (oldest-first).
- A sentinel `<div>` is prepended above the oldest message with:
  ```html
  <div hx-get="/rooms/{id}/messages?before=<oldest-timestamp-ms>&limit=50"
       hx-trigger="revealed"
       hx-swap="beforebegin">
  </div>
  ```
- When the sentinel scrolls into view, HTMX fetches the next 50 older messages and prepends them, replacing the sentinel with a new one (or removing it if no more messages exist). No JavaScript required.

### Redis Data Patterns

#### Message Storage
- On `POST /rooms/{id}/messages`:
  - Generate a message ID (e.g. UUIDv4 or timestamp-based).
  - `HSET messages:{msg-id} id ... room_id ... user_id ... text ... created_at ...`
  - `EXPIRE messages:{msg-id} 2592000` (30 days).
  - `ZADD rooms:{id}:messages <timestamp-ms> <msg-id>` to add to the room's index.
  - `ZREMRANGEBYSCORE rooms:{id}:messages 0 <timestamp-ms-30-days-ago>` to evict expired entries from the index.
  - `PUBLISH rooms:{id}:events <rendered-message-html>` for SSE fan-out.

#### Message Loading & Pagination
- Initial load: `ZREVRANGE rooms:{id}:messages 0 49` → fetch each message Hash → render oldest-first.
- Paginated history (`GET /rooms/{id}/messages?before=<timestamp-ms>&limit=50`): `ZREVRANGEBYSCORE rooms:{id}:messages (<timestamp-ms> -inf LIMIT 0 50` → fetch Hashes → return rendered HTML partial.
- For each message, check `unfurls:{sha256}` in Redis — include the preview card if cached, silently omit if not.

#### Link Previews
- On `POST /rooms/{id}/messages`, extract the first URL from the message text.
- Store and broadcast the message immediately (non-blocking).
- A goroutine then:
  1. Checks `unfurls:{sha256-of-normalised-url}` — if cached, publish the preview SSE event and return.
  2. Otherwise, calls the Microlink API (`https://api.microlink.io/?url=<url>`).
  3. Caches the result: `SET unfurls:{sha256} <json> EX 86400` (success) or `EX 900` (failure/no-data).
  4. Publishes a second SSE event (`event: unfurl`) with `data: <msg-id>:<rendered-unfurl-html>`. The vanilla JS `EventSource` listener on the client splits on the first `:` to extract the message ID and sets `innerHTML` on `#preview-<msg-id>`.
- On history load: Redis `GET unfurls:{sha256}` per message — preview included if cached, omitted if not. No fresh Microlink calls for history.
- DIY fallback path: if Microlink is dropped, replace the API call with `otiai10/opengraph v2` using an SSRF-hardened `*http.Client` (custom `DialContext` blocking RFC1918, loopback, link-local ranges; redirect re-validation; 1 MB response size limit; 5s timeout).

#### Sessions
- Sessions expire via Redis TTL (7 days, refreshed on each request).

### Authentication Flow
1. User clicks "Login with X" → `GET /auth/{provider}` → redirect to provider.
2. Provider redirects to `GET /auth/{provider}/callback`.
3. Exchange code for token, fetch user profile, upsert `users:{id}` in Redis.
4. Create session, set signed cookie, redirect to `/rooms/bemro`.
5. Logout: `POST /auth/logout` → delete session key, clear cookie.

### Startup — Room Seeding
On server start, seed the default room if it does not already exist:

```
ZADD NX rooms <unix-timestamp-seconds> "bemro"
HSET rooms:bemro id bemro name "Project BEMRØ"
```

The multi-room data model is fully operational from day one. Adding rooms in future requires only additional seeds or an admin command — no schema changes.

---

## What to Avoid
- No ORM. Use Redis commands directly through typed helpers.
- No client-side routing. Every navigation is a full or partial page load via HTMX.
- No global mutable state in Go beyond the Redis client and the HTTP server itself.
- Do not add a SQL database unless explicitly decided by the team.
- Do not add a frontend build step (no webpack, vite, etc.).
- No room-creation UI. Rooms are seeded at server startup only, until explicitly decided otherwise.
- Do not load CDN assets from providers other than jsDelivr without a deliberate decision.

---

## Running Locally

```sh
# Start Redis
docker run -d -p 6379:6379 redis:alpine

# Set environment variables (copy and fill in .env, then):
export $(cat .env | xargs)

# Run
go run ./...
```

---

## Definition of Done (per feature)

- Renders correctly with HTMX (no full-page reloads where a partial is expected).
- Works without JavaScript disabled for static content (progressive enhancement where practical).
- No secrets committed; all config via env vars.
- Redis keys follow the naming scheme above.
- New env vars are documented in this file.
