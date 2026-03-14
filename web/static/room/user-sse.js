// User-level SSE: listens on /user/events for cross-room notifications
// (unread badge increments). Follows the same lifecycle as room/sse.js.

function attachUserListeners(source) {
  source.addEventListener('unread', (e) => {
    let data;
    try { data = JSON.parse(e.data); } catch (_) { return; }
    const rid = data.roomId;
    if (!rid || rid === window.roomID) return;

    const badge = document.getElementById(`unread-badge-${rid}`);
    if (!badge) return;

    const current = parseInt(badge.textContent, 10) || 0;
    const next = current + 1;
    badge.textContent = next > 99 ? '99+' : String(next);
    badge.hidden = false;
  });
}

function openUserEs() {
  if (userEs) userEs.close();
  userEs = new EventSource('/user/events');
  attachUserListeners(userEs);
}

let userEs = null;
openUserEs();

// Clear badge on sidebar link click for instant feedback.
document.addEventListener('click', (e) => {
  const link = e.target.closest('.room-sidebar__link[data-room-id]');
  if (!link) return;
  const rid = link.dataset.roomId;
  const badge = document.getElementById(`unread-badge-${rid}`);
  if (badge) {
    badge.hidden = true;
    badge.textContent = '0';
  }
});

// Lifecycle: close on hide, reopen on visible/pageshow.
window.addEventListener('pagehide', () => { if (userEs) { userEs.close(); userEs = null; } });

document.addEventListener('visibilitychange', () => {
  if (document.hidden) {
    if (userEs) { userEs.close(); userEs = null; }
  } else {
    openUserEs();
  }
});

window.addEventListener('pageshow', (e) => {
  if (e.persisted) {
    openUserEs();
  }
});
