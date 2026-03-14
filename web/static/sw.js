// msg. Service Worker
// Handles Web Push notifications.
// Served at /sw.js (root scope) with no-cache headers.

// ---- Push event -------------------------------------------------------
// Called when the server sends a Web Push notification.
// If any tab has the app open and focused, we suppress the OS notification
// (the in-tab chime/toast already handles it).
self.addEventListener('push', (event) => {
  if (!event.data) return;

  let payload;
  try {
    payload = event.data.json();
  } catch (_e) {
    payload = { title: 'New message', body: event.data.text() };
  }

  // Data-only "clear" push: dismiss notifications for the given tag.
  if (payload.action === 'clear') {
    const tag = payload.tag;
    if (tag) {
      event.waitUntil(
        self.registration.getNotifications({ tag }).then((notifications) => {
          for (const n of notifications) n.close();
        }),
      );
    }
    return;
  }

  const title = payload.title || 'New message';
  const options = {
    body: payload.body || '',
    icon: payload.icon || '/static/logo_square_256.png',
    badge: '/static/logo_square_256.png',
    tag: payload.tag || 'msg-notification',
    data: { url: payload.url || '/' },
    // Reuse the same tag so rapid messages don't stack.
    renotify: true,
  };

  console.log('[sw] push received', title);

  event.waitUntil(
    // Check if any tab is currently visible (document.visibilityState === 'visible').
    // clients.matchAll returns all controlled clients (tabs/windows).
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      const hasVisibleTab = clients.some((c) => c.visibilityState === 'visible');

      if (hasVisibleTab) {
        // A tab is already showing the app — the in-tab notification handles it.
        return;
      }

      return self.registration.showNotification(title, options);
    }),
  );
});

// ---- Notification click -----------------------------------------------
// Focuses an existing tab or opens the room URL when the user taps
// a notification.
self.addEventListener('notificationclick', (event) => {
  event.notification.close();

  const targetURL = event.notification.data?.url || '/';

  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      // If there is already a tab on the same origin, focus it and navigate.
      for (let i = 0; i < clients.length; i++) {
        const client = clients[i];
        if ('focus' in client) {
          client.focus();
          if ('navigate' in client) {
            client.navigate(targetURL);
          }
          // Also postMessage so the client can self-navigate — client.navigate()
          // silently fails on iOS WebKit in standalone PWA mode.
          client.postMessage({ type: 'navigate', url: targetURL });
          return;
        }
      }
      // No existing tab — open a new one.
      if (self.clients.openWindow) {
        return self.clients.openWindow(targetURL);
      }
    }),
  );
});

// ---- Message from client ------------------------------------------------
// Handles postMessage from tabs (e.g. clear notifications on same device).
self.addEventListener('message', (event) => {
  if (event.data?.type === 'clear-notifications' && event.data.tag) {
    event.waitUntil(
      self.registration.getNotifications({ tag: event.data.tag }).then((notifications) => {
        for (const n of notifications) n.close();
      }),
    );
  }
});

// ---- Install / Activate -----------------------------------------------
// Minimal lifecycle: skip waiting so the new SW takes over immediately
// after install (important so push subscriptions stay valid across deploys).
self.addEventListener('install', () => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  // claim() rejects if a newer SW has already taken over by the time this
  // activate event runs (race during rapid installs). That's fine — the newer
  // SW will claim the clients instead, so we can safely swallow the error.
  // The spec mandates InvalidStateError; Chrome throws TypeError instead.
  // Any other error is unexpected and should propagate.
  event.waitUntil(
    self.clients.claim().catch((err) => {
      if (err instanceof TypeError || err.name === 'InvalidStateError') return;
      throw err;
    }),
  );
});
