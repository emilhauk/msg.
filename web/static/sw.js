// msg. Service Worker
// Handles Web Push notifications.
// Served at /sw.js (root scope) with no-cache headers.

'use strict';

// ---- Push event -------------------------------------------------------
// Called when the server sends a Web Push notification.
// If any tab has the app open and focused, we suppress the OS notification
// (the in-tab chime/toast already handles it).
self.addEventListener('push', function (event) {
  if (!event.data) return;

  var payload;
  try {
    payload = event.data.json();
  } catch (e) {
    payload = { title: 'New message', body: event.data.text() };
  }

  var title = payload.title || 'New message';
  var options = {
    body:  payload.body  || '',
    icon:  payload.icon  || '/static/logo_square_256.png',
    badge: '/static/logo_square_256.png',
    tag:   payload.tag   || 'msg-notification',
    data:  { url: payload.url || '/' },
    // Reuse the same tag so rapid messages don't stack.
    renotify: true,
  };

  event.waitUntil(
    // Check if any tab is currently visible (document.visibilityState === 'visible').
    // clients.matchAll returns all controlled clients (tabs/windows).
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function (clients) {
      var hasVisibleTab = clients.some(function (c) {
        return c.visibilityState === 'visible';
      });

      if (hasVisibleTab) {
        // A tab is already showing the app — the in-tab notification handles it.
        return;
      }

      return self.registration.showNotification(title, options);
    })
  );
});

// ---- Notification click -----------------------------------------------
// Focuses an existing tab or opens the room URL when the user taps
// a notification.
self.addEventListener('notificationclick', function (event) {
  event.notification.close();

  var targetURL = (event.notification.data && event.notification.data.url) || '/';

  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function (clients) {
      // If there is already a tab on the same origin, focus it and navigate.
      for (var i = 0; i < clients.length; i++) {
        var client = clients[i];
        if ('focus' in client) {
          client.focus();
          if ('navigate' in client) {
            client.navigate(targetURL);
          }
          return;
        }
      }
      // No existing tab — open a new one.
      if (self.clients.openWindow) {
        return self.clients.openWindow(targetURL);
      }
    })
  );
});

// ---- Install / Activate -----------------------------------------------
// Minimal lifecycle: skip waiting so the new SW takes over immediately
// after install (important so push subscriptions stay valid across deploys).
self.addEventListener('install', function () {
  self.skipWaiting();
});

self.addEventListener('activate', function (event) {
  event.waitUntil(self.clients.claim());
});
