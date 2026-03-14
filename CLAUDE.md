# msg — Project Guide

## Working Convention

CLAUDE.md contains stable project knowledge: architecture, decisions, conventions, schemas, and routes. Update it when a technical decision is made or changed, a non-obvious constraint is discovered, or a feature is added/changed/removed. No permission needed — treat it as a working document, not documentation.

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

## Project Layout

```
main.go                        # Entry point: wire routes, seed room, serve
internal/
  auth/
    oauth.go                   # OAuth flow; GitHub only; HandleLogin, HandleCallback, HandleLogout
    password.go                # Password auth; PasswordHandler.HandleLogin; POST /auth/password/login
    session.go                 # HMAC cookie sign/verify; SignToken, VerifyToken, SetCookie, ClearCookie
  handler/
    rooms.go                   # GET / → redirect /rooms/bemro; GET /rooms/{id}
    messages.go                # POST /rooms/{id}/messages (204); GET /rooms/{id}/messages (history)
                               #   PATCH /rooms/{id}/messages/{msgID} (author-only edit, 204)
                               #   DELETE /rooms/{id}/messages/{msgID} (author-only, 204)
                               #   hydrateMessages() — user+unfurl+reactions per message
                               #   sendPushNotifications() — async Web Push after save
    notifications.go           # Push subscribe/unsubscribe, mute, VAPID key, room members
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
  webpush/
    sender.go                  # Sender: Send(), SendToMany(); Config (VAPID keys)
  tmpl/
    render.go                  # Renderer; funcMap (renderText, linkify, reactionData, …); ChromaCSS
  testutil/
    testutil.go                # NewTestServer, NewTestServerWithPush, AuthCookie, SeedRoom, NoRedirectClient
  browser/
    browser_test.go            # E2E browser tests (go-rod / headless Chromium)
web/
  templates/
    base.html                  # Layout: HTMX, SSE ext, emoji-picker-element; theme/emoji/copy JS
    room.html                  # Room page; SSE wiring; all vanilla JS (scroll, upload, reactions, autocomplete)
    message.html               # Message partial (used for SSE push and initial render)
    reactions.html             # Reactions bar partial (used standalone for SSE reaction events)
    history.html               # Infinite-scroll history partial (sentinel + messages)
    unfurl.html                # Link preview card partial
    login.html                 # Login page (GitHub button; optional password form via PasswordAuthEnabled)
    error.html                 # Error page
  static/
    style.css                  # All styles
    favicon.svg
    sw.js                      # Service Worker (served at /sw.js, root scope, no-cache)
    chime.mp3                  # In-tab notification chime sound
cmd/
  createuser/main.go           # CLI: provision a password-auth account (email, name, bcrypt hash)
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
POST /auth/password/login                  — email+password login (only when ENABLE_PASSWORD_LOGIN=true)

GET  /                                     — redirect → /rooms/bemro
GET  /rooms/{id}                           — room page (last 50 msgs)
GET  /rooms/{id}/events                    — SSE stream (Redis Pub/Sub)
POST /rooms/{id}/messages                  — post message → 204 (SSE delivers to all)
GET  /rooms/{id}/messages?before=<ms>&limit=50  — paginated history partial
PATCH   /rooms/{id}/messages/{msgID}       — edit own message → 204 + SSE edit event
DELETE  /rooms/{id}/messages/{msgID}       — delete own message → 204 + SSE delete event
POST /rooms/{id}/messages/{msgID}/reactions — toggle emoji reaction → 204 + SSE reaction event
GET  /rooms/{id}/members                   — room member list for @mention autocomplete
DELETE /rooms/{id}/leave                   — leave room; if last member, deletes room + all messages → redirect /
POST /rooms/{id}/active                    — record user as actively viewing room (updates last_active + viewing key)
POST /rooms/{id}/inactive                  — clear viewing key immediately (called via sendBeacon on hide)
GET  /rooms/{id}/upload-url?hash=&content_type=&content_length=  — presign S3 PUT (optional)

GET  /push/vapid-public-key                — VAPID public key (unauthenticated)
POST /push/subscribe                       — save Web Push subscription
DELETE /push/subscribe                     — remove Web Push subscription
GET  /settings/mute                        — get current mute state
POST /settings/mute                        — set mute duration (1h/8h/24h/168h/forever)
DELETE /settings/mute                      — clear mute

GET  /user/events                          — user-level SSE stream (unread badges, future cross-room events)

GET  /sw.js                                — Service Worker (root scope; no-cache)
GET  /static/*                             — embedded static files (immutable cache)
GET  /static/chroma.css                    — generated syntax-highlight CSS
```

---

## Redis Key Schema

```
sessions:{token}                        Hash    id, name, avatar_url; TTL 90 days
oauth:state:{state}                     String  CSRF state; TTL 10 min (consumed on use)
users:{uuid}                            Hash    id, name, avatar_url, email, created_at (no TTL)
users:{uuid}:identities                 Set     "{provider}:{providerUserID}" members (no TTL)
users:{uuid}:push_subscriptions         Hash    endpoint → subscriptionJSON (no TTL)
users:{uuid}:mute_until                 String  unix ms timestamp or "forever"; TTL = mute duration (or none)
identities:{provider}:{providerUserID}  String  canonical uuid (no TTL)
rooms                                   ZSet    room IDs scored by creation time (unix seconds)
rooms:{id}                              Hash    id, name
rooms:{id}:messages                     ZSet    message IDs scored by created_at (unix ms); cleaned on write
rooms:{id}:members                      ZSet    user IDs scored by last-post time (unix ms); no TTL
rooms:{id}:events                       Pub/Sub SSE fan-out channel
users:{uuid}:events                     Pub/Sub user-level SSE channel (unread badges, future cross-room events)
messages:{msg-id}                       Hash    id, room_id, user_id, text, kind, attachments (JSON), created_at (ms); TTL 30 days
reactions:{msg-id}                      Hash    emoji → count; TTL 30 days
reactions:{msg-id}:users                Hash    "{emoji}\x00{userID}" → "1"; TTL 30 days
reactions:{msg-id}:order                ZSet    emoji members scored by unix-ms of first-use; TTL 30 days
unfurls:{sha256-of-url}                 String  JSON Unfurl or "null"; TTL 24h (success) / 15 min (failure)
users:{uuid}:rooms:{roomId}:last_active String  unix ms timestamp; TTL 30 days (reset on each write)
users:{uuid}:rooms:{roomId}:viewing    String  "1"; TTL 90 s; reset by heartbeat every 60 s; cleared immediately by leave beacon
users:{uuid}:password                   String  bcrypt hash (cost 12); no TTL; only set for password-auth accounts
email_index:{email}                     String  canonical uuid; no TTL; written by createuser CLI
```

### Identity model

`user_id` stored in messages, reactions, and sessions is always a **UUID v4** (the canonical user ID). OAuth provider identities (e.g. `github:12345678`) live only in the `identities:` and `users:{uuid}:identities` keys and are never used as a user identifier elsewhere.

- `users:{uuid}` has no `provider` field — provider is identity-level, not user-level.
- Name and avatar are seeded from the first provider login and refreshed on each subsequent login via the same provider.
- Email is **sensitive**: must only be exposed by profile-page endpoints (not yet implemented). Do not include it in session hashes, SSE payloads, or any response not scoped to the authenticated user's own profile.
- Linking a second provider is done explicitly by a logged-in user (via `redis.LinkIdentity`); there is no automatic email-based merge.

---

## SSE Payload Protocol

All payloads published to `rooms:{id}:events` use a prefix to identify type:

```
"msg:<html>"             → event: message   (HTMX sse-swap into #sse-message-target)
"unfurl:<msgId>:<html>"  → event: unfurl    (JS: innerHTML on #preview-<msgId>)
"reaction:<json>"        → event: reaction  (JS: JSON { msgId, reactorId, reactedEmojis, html })
"delete:<msgId>"         → event: delete    (JS: remove #msg-<msgId>)
"edit:<msgId>:<html>"    → event: edit      (JS: replace #msg-<msgId> innerHTML)
"memberstatus:<json>"    → event: memberstatus  (HTMX: re-fetch panel; JS: update own bell)
```

Payloads published to `users:{uuid}:events` (user-level channel):

```
"unread:<json>"          → event: unread   (JS: increment badge for { roomId })
```

On connect the server also sends:
```
event: version\ndata: <buildSHA>
```
Used by the client to detect deploys and trigger a reload.

---

## Three SSE Connections Per Client

The room page opens **three** `EventSource` connections:

1. **HTMX-managed** (`sse-connect` on `<section>`) → `/rooms/{id}/events`: handles `event: message` only via `sse-swap`.
2. **Vanilla JS-managed** (`room/sse.js`) → `/rooms/{id}/events`: handles `unfurl`, `reaction`, `delete`, `edit`, `memberstatus`, and `version`.
3. **User-level** (`room/user-sse.js`) → `/user/events`: handles `unread` badge increments for other rooms.

Rationale for #1/#2 split: HTMX's SSE extension silently drops event types it is not `sse-swap`-ing.
Rationale for #3: user-level events (unread counts, future invites/DMs) are not room-scoped.

---

## Message ID Format

```
{unixMillis}-{userUUID}
```
e.g. `1712345678901-550e8400-e29b-41d4-a716-446655440000`. The user portion is always a UUID v4 — never a provider-specific identifier.

---

## Message Kind

The `kind` field on a message hash distinguishes message types:

| Kind | Meaning |
|---|---|
| `""` (empty) | Regular user message (default, backward compatible) |
| `"system"` | System notification (join/leave/added) |

System messages:
- Are created by `saveAndBroadcastSystemMessage()` in `internal/handler/messages.go`
- Rendered as centered text with horizontal rules (`.message--system` in CSS)
- Cannot be edited or deleted (handlers return 403)
- Skip unfurl and reaction hydration
- Preserve the real `UserID` for attribution; `Text` carries the display string

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

## SSE Broadcast Neutrality

SSE HTML is sent to **every** connected client verbatim — it must never contain per-viewer state.

**Reactions** — broadcast as neutral HTML (no `ReactedByMe` baked in) plus the reacting user's emoji list. Each client applies its own active state from `__myReactions` in JS, then calls `htmx.process()` on the swapped element.

**Owner controls (edit/delete buttons)** — always rendered in `message.html` with `hidden` by default. SSE publishes always pass `CurrentUserID: ""`. After every DOM insertion the client calls `applyOwnerControls(articleEl)`, which compares `articleEl.dataset.authorId` against `window.__currentUserID` and removes `hidden` when they match.

---

## Key Conventions

### Go
- Handlers are thin; business logic lives in `internal/` packages.
- `POST /rooms/{id}/messages` always returns `204 No Content`. Never return rendered HTML from POST — the message is delivered exclusively via SSE to avoid duplicates.
- All Redis operations go through typed helpers in `internal/redis/client.go`. Never scatter raw Redis commands in handlers.
- `context.Context` is threaded through all Redis and HTTP calls for proper cancellation.
- `middleware.UserFromContext(ctx)` retrieves the authenticated user; always available in protected routes.
- `model.MessageView` wraps `*model.Message` + `CurrentUserID` for templates that conditionally render owner-only controls.
- `Renderer.RenderString()` renders partials (SSE fragments) without the base layout and without wrapping in `pageData`.
- `Renderer.Render()` wraps data in `pageData{BuildVersion, Data}` — templates access `.Data.*` and `.BuildVersion`.
- New templates must be registered in `tmpl.New()` in `internal/tmpl/render.go`.

### CDN Policy
jsDelivr only. No other CDN without deliberate decision.

### No build step
No webpack, vite, or any frontend bundler. No TypeScript compilation. No Tailwind. Static files are embedded via `//go:embed web`.

---

## Decisions & Constraints

- **GitHub OAuth only.** Handler rejects non-GitHub providers. Don't add Google/Discord without a deliberate decision.
- **No room-creation UI.** Rooms are seeded at startup only (`SeedRoom` in `main.go`). `bemro` is the only active room.
- **No ORM. No SQL.** Redis only, through `internal/redis/client.go`.
- **No client-side routing.** Every navigation is a full or partial (HTMX) page load.
- **Sessions TTL: 90 days** — cookie + Redis TTL kept in sync; refreshed on every authenticated request.
- **Message TTL: 30 days** — both the Hash and the sorted-set entry are cleaned up.
- **Syntax highlighting** uses Chroma (server-side). CSS generated at startup for `github` (light) and `github-dark` (dark) themes; served from `/static/chroma.css`; integrates with the `[data-theme]` attribute system.
- **Emoji shortcode autocomplete** uses `emoji-picker-element`'s `Database` class exposed as `window.__EmojiDatabase` from the module script in `base.html`. The room page polls for it with `setInterval`.

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

MICROLINK_API_KEY      optional; only needed above Microlink free-tier limits

S3_ENDPOINT            omit to disable media uploads (e.g. https://s3.example.com)
S3_BUCKET
S3_REGION              MinIO accepts any string (e.g. us-east-1)
S3_ACCESS_KEY_ID
S3_SECRET_ACCESS_KEY

VAPID_PUBLIC_KEY       base64url P-256 public key; omit to disable Web Push
VAPID_PRIVATE_KEY      base64url P-256 private key
VAPID_SUBJECT          contact URI, e.g. mailto:admin@yourdomain.com

ENABLE_PASSWORD_LOGIN  "true" to enable email+password login; unset/false = completely hidden
```

---

## Icon Generation

PWA icons are generated from `favicon.svg` using `rsvg-convert`. Do **not** use `logo_square_256.png` as source — the SVG is the source of truth.

Parameters (chosen via visual review — do not change without re-reviewing):
- Background: `#5865f2` rounded square, `rx="22.5"` on 100×100 canvas
- Padding: 15% each side; vertical optical nudge: +4.5 units downward

```sh
python3 - <<'EOF'
import subprocess
pad, offset_y = 15, 4.5
scale = round((100 - 2 * pad) / 22.0, 6)
svg = f"""<svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg">
  <rect width="100" height="100" rx="22.5" fill="#5865f2"/>
  <g transform="translate({pad},{pad + offset_y}) scale({scale})">
    <rect x="0" y="0" width="22" height="16" rx="4" fill="white"/>
    <circle cx="5.5"  cy="8" r="1.75" fill="#5865f2"/>
    <circle cx="11"   cy="8" r="1.75" fill="#5865f2"/>
    <circle cx="16.5" cy="8" r="1.75" fill="#5865f2"/>
    <path d="M4 15.5 L2 20 L9 15.5" fill="white" stroke-linejoin="round"/>
  </g>
</svg>"""
open('/tmp/logo_icon.svg', 'w').write(svg)
for px, name in [(180, 'apple-touch-icon.png'), (192, 'logo_192.png'), (512, 'logo_512.png')]:
    subprocess.run(['rsvg-convert', '-w', str(px), '-h', str(px), '/tmp/logo_icon.svg',
                    '-o', f'web/static/{name}'], check=True)
    print(f'Generated web/static/{name}')
EOF
```

---

## Build

Build version is injected at build time:
```sh
go build -ldflags "-X main.buildVersion=$(git rev-parse --short HEAD)" .
```

---

## Testing

```sh
make test                                              # lint + Go (short) + E2E browser
go test ./... -race -timeout 60s -count=1 -short      # unit/integration only
go test ./internal/browser/... -v -timeout 120s       # E2E browser (go-rod / headless Chromium)
npm run lint                                           # JS linting (Biome)
```

**Shared test helpers** — `internal/testutil/testutil.go`:
- `NewTestServer(t)` — miniredis + redis client + full mux + `httptest.Server`; all notification routes always wired
- `NewTestServerWithPush(t, sender)` — same, but wires a push sender for dispatch tests
- `ts.AuthCookie(t, user)` — seeds a session directly into miniredis, returns signed cookie
- `ts.SeedRoom(t, room)` — seeds a room record
- `testutil.NoRedirectClient()` — `*http.Client` that stops at 302

**Key patterns:**
- POST/DELETE/PATCH/reaction toggle all return **204**; tests assert status + verify pub/sub payload by subscribing before issuing the request.
- SSE pub/sub relay: read initial events first, then `time.Sleep(50ms)` to let the subscription register, then publish.
- Password tests use `bcrypt.MinCost` for speed.
- Browser SW tests: set up `browser.EachEvent` **before** calling `proto.ServiceWorkerEnable{}.Call(page)` to avoid missing the `workerVersionUpdated` event.
