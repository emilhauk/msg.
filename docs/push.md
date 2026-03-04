# Web Push Architecture

## The Players

```
┌─────────────────┐     ┌──────────────────────┐     ┌───────────────────┐
│   Your Server   │     │  Apple Push Network  │     │  iPhone / Safari  │
│  (msg app)      │     │  (APNs)              │     │  PWA              │
└─────────────────┘     └──────────────────────┘     └───────────────────┘
```

---

## Phase 1 — Subscription (one-time setup)

```
iPhone                          Your Server
  │                                  │
  │  User opens PWA, taps 🔔         │
  │                                  │
  │── pushManager.subscribe() ──────▶│  (browser API call)
  │                                  │
  │  iOS contacts Apple to get       │
  │  a unique device token           │
  │  ┌─────────────────────────┐     │
  │  │ APNs registration...    │     │
  │  └─────────────────────────┘     │
  │                                  │
  │  Returns PushSubscription:       │
  │  {                               │
  │    endpoint: "https://web.push.apple.com/...<device-token>",
  │    keys: {                       │
  │      p256dh: "<your ECDH pubkey>",  ← used to encrypt payload
  │      auth:   "<16-byte secret>"     ← used to encrypt payload
  │    }                             │
  │  }                               │
  │                                  │
  │── POST /push/subscribe ─────────▶│
  │   (sends the subscription JSON)  │
  │                                  │
  │                         stores in Redis:
  │                         users:{uuid}:push_subscriptions
  │                         endpoint → subscriptionJSON
```

---

## Phase 2 — Delivery (every notification)

```
Your Server                      APNs                         iPhone
     │                             │                              │
     │  Someone posts a message    │                              │
     │                             │                              │
     │  Fetch subscriptions        │                              │
     │  from Redis                 │                              │
     │                             │                              │
     │  For each subscription:     │                              │
     │  1. Encrypt payload         │                              │
     │     (RFC 8291 aes128gcm)    │                              │
     │     using p256dh + auth     │                              │
     │                             │                              │
     │  2. Build VAPID JWT         │                              │
     │     signed with your        │                              │
     │     VAPID private key       │                              │
     │                             │                              │
     │── POST https://web.push.apple.com/...<token> ────────────▶│
     │   Authorization: vapid t=<JWT>,k=<pubkey>                 │
     │   Content-Encoding: aes128gcm                             │
     │   TTL: 86400                                              │
     │   [encrypted body]          │                              │
     │                             │                              │
     │              APNs validates JWT signature                  │
     │              APNs decrypts the routing info                │
     │              (but NOT the payload — only iPhone can)       │
     │                             │                              │
     │                             │── wake up iPhone ───────────▶│
     │                             │   [still-encrypted payload]  │
     │                             │                              │
     │◀── 201 Created ────────────│                              │
     │    (or 4xx with JSON error) │                              │
     │                             │              Service Worker wakes up
     │                             │              sw.js: push event fires
     │                             │              decrypts payload using
     │                             │              p256dh private key
     │                             │              shows notification 🔔
```

---

## iOS vs Chrome

```
Chrome PWA                          iOS Safari PWA
───────────────────────────────     ────────────────────────────────
Push service: Google FCM            Push service: Apple APNs
Endpoint:     https://fcm.google…   Endpoint:     https://web.push.apple.com/…

Both use the same Web Push protocol (RFC 8030/8291/8292).
webpush-go handles both identically.
APNs tends to be stricter about VAPID JWT validity.
```

---

## Debugging Rejected Pushes

APNs returns a JSON error body on rejection. Common reasons:

| Reason | Meaning | Fix |
|---|---|---|
| `BadJWTToken` | VAPID JWT invalid or expired | Check VAPID key format and clock skew |
| `BadDeviceToken` | Device token no longer valid | Remove subscription from Redis (treat as 410 Gone) |
| `InvalidHeaders` | Missing or malformed headers | Library bug or misconfigured options |
| `ExpiredSubscription` | Subscription has expired | Remove subscription from Redis |

Server logs will show the rejection with endpoint, status, and reason:

```
webpush: push rejected endpoint=https://web.push.apple.com/... status=403 body={"reason":"BadJWTToken"}
```

To trigger a test: background the iPhone PWA and post a message from another device, then:

```sh
docker compose logs -f app
```

---

## Code References

| File | Purpose |
|---|---|
| `internal/webpush/sender.go` | `Send` / `SendToMany` — encrypts and POSTs to push endpoint |
| `internal/handler/notifications.go` | Subscribe / unsubscribe / mute routes |
| `internal/handler/messages.go` | `sendPushNotifications()` — called async after message save |
| `internal/redis/client.go` | `SavePushSubscription`, `GetPushSubscriptions`, `DeletePushSubscription` |
| `web/static/sw.js` | Service Worker — handles `push` event, shows notification |
