import { attachImageLoadSnap } from '/static/room/scroll.js';
import { applyOwnerControls } from '/static/room/owner-controls.js';
import { __myReactions, applyMyReactions } from '/static/room/reactions.js';

// Second EventSource: handles unfurl, reaction, delete, edit, and version SSE events.
// HTMX manages its own EventSource for "message" events; custom event types
// use a dedicated connection so we can use native addEventListener.

// -- Auto-reload on new deploy ------------------------------------------
// On first connect the server sends the running build SHA; on reconnect
// after a deploy it sends a different SHA. We react based on focus state:
//   • tab not focused  → reload immediately (silent, user won't notice)
//   • after next send  → reload once the message form posts successfully
//   • otherwise        → show the #update-hint button near the logo
let __serverVersion = null;
let __pendingReload = false;
let __catchUpInProgress = false;

async function doCatchUp() {
  if (__catchUpInProgress) return;
  __catchUpInProgress = true;
  const spinner = document.getElementById('history-spinner');
  spinner?.classList.add('is-loading');
  try {
    const res = await fetch(`/rooms/${window.roomID}/messages?limit=50`);
    if (!res.ok) return;

    const html = await res.text();
    const temp = document.createElement('div');
    temp.innerHTML = html;

    const newArticles = [...temp.querySelectorAll('article.message')];
    if (newArticles.length === 0) return;

    const content = document.getElementById('message-list-content');
    const target = document.getElementById('sse-message-target');
    if (!content || !target) return;

    const hasOverlap = newArticles.some(a => document.getElementById(a.id));

    if (!hasOverlap) {
      // Big gap: clear stale messages and any existing sentinel.
      content.querySelectorAll('article.message, .scroll-sentinel').forEach(el => { el.remove(); });
      // Restore a sentinel from the catch-up response if present.
      const newSentinel = temp.querySelector('.scroll-sentinel');
      if (newSentinel) {
        target.before(newSentinel);
        htmx.process(newSentinel);
      }
    }

    // Insert new (non-duplicate) articles before the SSE target.
    for (const article of newArticles) {
      if (!document.getElementById(article.id)) {
        target.before(article);
        htmx.process(article);
        applyOwnerControls(article);
      }
    }

    // Snap to bottom so the user sees the freshest messages.
    const list = document.getElementById('message-list');
    if (list) list.scrollTop = list.scrollHeight;
  } catch (_) {
    // network error or body-read failure — nothing to do
  } finally {
    spinner?.classList.remove('is-loading');
    __catchUpInProgress = false;
  }
}

function attachEsListeners(target) {
  target.addEventListener('version', (e) => {
    if (__serverVersion === null) {
      __serverVersion = e.data;
      return;
    }
    if (e.data === __serverVersion) {
      doCatchUp();
      return;
    }

    if (!document.hasFocus()) {
      window.location.reload();
      return;
    }
    __pendingReload = true;
    const hint = document.getElementById('update-hint');
    if (hint) hint.hidden = false;
  });

  target.addEventListener('delete', (e) => {
    const el = document.getElementById(`msg-${e.data.trim()}`);
    if (el) el.remove();
  });

  target.addEventListener('edit', (e) => {
    // data format: "<msgId>\n<html>" (two SSE data lines joined by \n)
    const nl = e.data.indexOf('\n');
    if (nl < 0) return;
    const msgId = e.data.slice(0, nl);
    const html = e.data.slice(nl + 1);
    const el = document.getElementById(`msg-${msgId}`);
    if (!el) return;
    el.outerHTML = html;
    // Re-process HTMX on the swapped-in element so hx-delete/hx-patch work.
    const newEl = document.getElementById(`msg-${msgId}`);
    if (newEl) {
      htmx.process(newEl);
      applyOwnerControls(newEl);
    }
  });

  target.addEventListener('unfurl', (e) => {
    // data format: "<msgId>\n<html>" (two SSE data lines joined by \n)
    const nl = e.data.indexOf('\n');
    if (nl < 0) return;
    const msgId = e.data.slice(0, nl);
    const html = e.data.slice(nl + 1);
    const el = document.getElementById(`preview-${msgId}`);
    if (el) {
      el.innerHTML = html;
      attachImageLoadSnap(el);
    }
  });

  target.addEventListener('reaction', (e) => {
    // data is a JSON object: { msgId, reactorId, reactedEmojis, html }
    let ev;
    try {
      ev = JSON.parse(e.data);
    } catch (_) {
      return;
    }
    const el = document.getElementById(`reactions-${ev.msgId}`);
    if (!el) return;

    // If the reactor is the current user, update our local reaction map.
    if (ev.reactorId === window.__currentUserID) {
      __myReactions[ev.msgId] = new Set(ev.reactedEmojis || []);
    }

    // Swap in the neutral HTML (no active state baked in).
    const temp = document.createElement('div');
    temp.innerHTML = ev.html;
    const newEl = temp.firstElementChild;
    if (!newEl) return;

    // Re-apply the current user's active styling from our local map.
    applyMyReactions(newEl, ev.msgId);

    el.replaceWith(newEl);
    // Re-process HTMX on the new element so hx-post works on the pills.
    htmx.process(newEl);
  });
}

let es = new EventSource(`/rooms/${window.roomID}/events`);
attachEsListeners(es);

// Reload after a successful message send if a deploy was detected.
const form = document.querySelector('.message-form');
if (form) {
  form.addEventListener('htmx:afterRequest', () => {
    if (__pendingReload) window.location.reload();
  });
}

// Close the EventSource when the page hides; reopen immediately when it
// becomes visible again so reconnect happens without browser backoff delay.
window.addEventListener('pagehide', () => { es.close(); });

document.addEventListener('visibilitychange', () => {
  if (document.hidden) {
    es.close();
  } else {
    es = new EventSource(`/rooms/${window.roomID}/events`);
    attachEsListeners(es);
    doCatchUp();
  }
});

window.addEventListener('pageshow', (e) => {
  if (e.persisted) { // restored from bfcache
    es = new EventSource(`/rooms/${window.roomID}/events`);
    attachEsListeners(es);
    doCatchUp();
  }
});

window.addEventListener('online', () => { doCatchUp(); });
