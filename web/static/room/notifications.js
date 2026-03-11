// ---- Notifications: SW registration, push subscription, mute, leader election, chime ----
// Also handles the profile popover and unread message count.

// ------------------------------------------------------------------
// BroadcastChannel leader election
// The tab with the most recent heartbeat timestamp is the "leader".
// Only the leader plays the in-tab chime / shows an in-page toast.
// ------------------------------------------------------------------
const TAB_ID = Math.random().toString(36).slice(2);
const lastHeartbeats = {}; // tabId → timestamp
lastHeartbeats[TAB_ID] = Date.now();

let bc;
try {
  bc = new BroadcastChannel('msg-notifications');
} catch (_) {
  bc = null;
}

function broadcastHeartbeat() {
  lastHeartbeats[TAB_ID] = Date.now();
  if (bc) bc.postMessage({ type: 'heartbeat', tabId: TAB_ID, ts: Date.now() });
}

function isLeader() {
  const myTs = lastHeartbeats[TAB_ID] || 0;
  for (const id in lastHeartbeats) {
    if (id !== TAB_ID && lastHeartbeats[id] > myTs) return false;
  }
  return true;
}

if (bc) {
  bc.onmessage = (e) => {
    if (e.data && e.data.type === 'heartbeat') {
      lastHeartbeats[e.data.tabId] = e.data.ts;
    }
    if (e.data && e.data.type === 'mute') {
      if (e.data.muted) {
        setBellState('muted', e.data.until);
        if (unmuteBtn) unmuteBtn.hidden = false;
      } else {
        setBellState('on');
        if (unmuteBtn) unmuteBtn.hidden = true;
      }
    }
  };
}

document.addEventListener('visibilitychange', broadcastHeartbeat);
window.addEventListener('focus', broadcastHeartbeat);
broadcastHeartbeat();
setInterval(broadcastHeartbeat, 5000);

// ------------------------------------------------------------------
// In-tab chime + toast
// ------------------------------------------------------------------
let chimeAudio = null;

function playChime() {
  try {
    if (!chimeAudio) chimeAudio = new Audio(window.CHIME_URL);
    chimeAudio.cloneNode().play().catch(() => {});
  } catch (_) {}
}

function shouldNotifyInTab() {
  return isLeader() && document.hidden;
}

document.body.addEventListener('htmx:sseMessage', () => {
  if (shouldNotifyInTab()) {
    playChime();
  }
});

// ------------------------------------------------------------------
// Service Worker registration
// ------------------------------------------------------------------
let swRegistration = null;
const isIOS =
  /iPhone|iPad|iPod/.test(navigator.userAgent) ||
  (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
const needsPWAGuide = isIOS && navigator.standalone !== true;

const bell = document.getElementById('notif-bell');
const popover = document.getElementById('notif-popover');
const unmuteBtn = document.getElementById('notif-unmute');
const unsubBtn = document.getElementById('notif-unsubscribe');
const pwaGuide = document.getElementById('ios-pwa-guide');

if (needsPWAGuide) {
  setBellState('off');
} else if ('serviceWorker' in navigator) {
  // Secondary update trigger: fires when a new SW takes over via
  // skipWaiting + clients.claim.
  const prevController = navigator.serviceWorker.controller;
  navigator.serviceWorker.addEventListener('controllerchange', () => {
    if (!prevController) return; // first install — not a real update
    if (!document.hasFocus()) {
      window.location.reload();
      return;
    }
    const hint = document.getElementById('update-hint');
    if (hint) hint.hidden = false;
  });

  navigator.serviceWorker
    .register('/sw.js', { scope: '/' })
    .then((reg) => {
      swRegistration = reg;
      reg.pushManager.getSubscription().then((sub) => {
        if (sub) {
          setBellState('on');
          checkMuteState();
        } else {
          setBellState('off');
        }
      });
    })
    .catch((err) => {
      console.warn('SW registration failed:', err);
    });
} else {
  setBellState('off');
}

// ------------------------------------------------------------------
// Bell UI state
// ------------------------------------------------------------------
if (pwaGuide) {
  function closeGuide() {
    pwaGuide.classList.add('is-closing');
    pwaGuide.addEventListener('animationend', function handler() {
      pwaGuide.removeEventListener('animationend', handler);
      pwaGuide.classList.remove('is-closing');
      pwaGuide.close();
    });
  }

  pwaGuide.querySelector('.pwa-guide__close').addEventListener('click', closeGuide);
  pwaGuide.addEventListener('click', (e) => {
    if (e.target === pwaGuide) closeGuide();
  });
}

window.setBellState = setBellState;
function setBellState(state, muteUntil) {
  if (!bell) return;
  bell.dataset.state = state;
  bell.disabled = state === 'loading' || state === 'pending';
  const labels = {
    off: 'Enable notifications',
    on: 'Notification settings',
    muted: 'Notifications muted — click to manage',
    loading: 'Enabling notifications…',
  };
  bell.setAttribute('aria-label', labels[state] || 'Notifications');
  if (state === 'muted' && muteUntil) {
    bell.dataset.muteUntil = muteUntil;
  } else {
    delete bell.dataset.muteUntil;
    bell.removeAttribute('title');
  }
}

function formatMuteRemaining(muteUntil) {
  if (!muteUntil || muteUntil === 'forever') return 'Muted indefinitely';
  const ms = new Date(muteUntil).getTime() - Date.now();
  if (ms <= 0) return null;
  const mins = Math.round(ms / 60000);
  const hours = Math.round(ms / 3600000);
  const days = Math.round(ms / 86400000);
  if (days >= 2) return `Muted for ${days} more days`;
  if (hours >= 2) return `Muted for ${hours} more hours`;
  if (mins >= 2) return `Muted for ${mins} more minutes`;
  return 'Muted for less than a minute';
}

if (bell) {
  bell.addEventListener('mouseenter', () => {
    if (bell.dataset.state !== 'muted') return;
    const text = formatMuteRemaining(bell.dataset.muteUntil);
    if (text) bell.setAttribute('title', text);
    else bell.removeAttribute('title');
  });
}

function checkMuteState() {
  fetch('/settings/mute', { credentials: 'same-origin' })
    .then((r) => r.json())
    .then((data) => {
      if (data.muted) {
        setBellState('muted', data.until);
        if (unmuteBtn) unmuteBtn.hidden = false;
      } else {
        setBellState('on');
        if (unmuteBtn) unmuteBtn.hidden = true;
      }
    })
    .catch(() => {});
}

// ------------------------------------------------------------------
// Push subscription helpers
// ------------------------------------------------------------------
function urlBase64ToUint8Array(base64String) {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
  const rawData = atob(base64);
  const output = new Uint8Array(rawData.length);
  for (let i = 0; i < rawData.length; i++) output[i] = rawData.charCodeAt(i);
  return output;
}

function subscribeForPush() {
  if (!swRegistration) return Promise.reject(new Error('SW not ready'));
  return fetch('/push/vapid-public-key', { credentials: 'same-origin' })
    .then((r) => r.json())
    .then((data) =>
      swRegistration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(data.key),
      }),
    )
    .then((sub) =>
      fetch('/push/subscribe', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(sub.toJSON()),
      }).then(() => sub),
    );
}

function unsubscribeFromPush() {
  if (!swRegistration) return Promise.resolve();
  return swRegistration.pushManager.getSubscription().then((sub) => {
    if (!sub) return;
    const endpoint = sub.endpoint;
    return sub.unsubscribe().then(() =>
      fetch('/push/subscribe', {
        method: 'DELETE',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ endpoint: endpoint }),
      }),
    );
  });
}

// ------------------------------------------------------------------
// Bell click handler
// ------------------------------------------------------------------
if (bell) {
  bell.addEventListener('click', (e) => {
    e.stopPropagation();
    const state = bell.dataset.state;

    if (state === 'off') {
      if (needsPWAGuide) {
        pwaGuide?.showModal();
        return;
      }
      setBellState('loading');
      Notification.requestPermission().then((perm) => {
        if (perm !== 'granted') {
          setBellState('off');
          return;
        }
        subscribeForPush()
          .then(() => { setBellState('on'); })
          .catch((err) => {
            console.warn('Push subscribe failed:', err);
            setBellState('off');
          });
      });
      return;
    }

    // Subscribed or muted — toggle popover.
    if (popover) {
      popover.hidden = !popover.hidden;
    }
  });
}

// Mute duration buttons.
const MUTE_MS = { '1h': 3600000, '8h': 28800000, '24h': 86400000, '168h': 604800000 };
if (popover) {
  popover.querySelectorAll('[data-mute]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const dur = btn.dataset.mute;
      const until =
        dur === 'forever' ? 'forever' : new Date(Date.now() + MUTE_MS[dur]).toISOString();
      fetch('/settings/mute', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ duration: dur }),
      }).then(() => {
        setBellState('muted', until);
        if (unmuteBtn) unmuteBtn.hidden = false;
        if (popover) popover.hidden = true;
        if (bc) bc.postMessage({ type: 'mute', muted: true, until: until });
      });
    });
  });
}

// Unmute button.
if (unmuteBtn) {
  unmuteBtn.addEventListener('click', () => {
    fetch('/settings/mute', { method: 'DELETE', credentials: 'same-origin' }).then(() => {
      setBellState('on');
      unmuteBtn.hidden = true;
      if (popover) popover.hidden = true;
      if (bc) bc.postMessage({ type: 'mute', muted: false });
    });
  });
}

// Turn off notifications (unsubscribe).
if (unsubBtn) {
  unsubBtn.addEventListener('click', () => {
    unsubscribeFromPush().then(() => {
      setBellState('off');
      if (popover) popover.hidden = true;
    });
  });
}

// Close popover on outside click.
document.addEventListener('click', (e) => {
  if (popover && !popover.hidden) {
    const wrap = document.getElementById('notif-wrap');
    if (wrap && !wrap.contains(e.target)) popover.hidden = true;
  }
});

// ------------------------------------------------------------------
// Profile popover
// ------------------------------------------------------------------
const profileBtn = document.getElementById('profile-btn');
const profilePopover = document.getElementById('profile-popover');
if (profileBtn && profilePopover) {
  profileBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    profilePopover.hidden = !profilePopover.hidden;
  });

  document.addEventListener('click', (e) => {
    if (!profilePopover.hidden) {
      const wrap = document.getElementById('profile-wrap');
      if (wrap && !wrap.contains(e.target)) profilePopover.hidden = true;
    }
  });
}

// ------------------------------------------------------------------
// Unread message count (title prefix + favicon badge)
// ------------------------------------------------------------------
let unreadCount = 0;
const originalTitle = document.title;
const faviconEl = document.querySelector('link[rel="icon"]');
const FAVICON_NORMAL = faviconEl ? faviconEl.href : null;
const FAVICON_BADGE = '/static/favicon-badge.svg';

function setUnread(n) {
  unreadCount = n;
  document.title = n > 0 ? `[${n}] ${originalTitle}` : originalTitle;
  if (faviconEl) faviconEl.href = n > 0 ? FAVICON_BADGE : FAVICON_NORMAL;
}

document.body.addEventListener('htmx:sseMessage', () => {
  if (!document.hidden) return;
  const target = document.getElementById('sse-message-target');
  const msg = target?.previousElementSibling;
  if (msg && msg.dataset.authorId !== window.__currentUserID) {
    setUnread(unreadCount + 1);
  }
});

document.addEventListener('visibilitychange', () => {
  if (!document.hidden) setUnread(0);
});
