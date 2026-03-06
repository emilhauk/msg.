// __myReactions tracks which emojis the current user has reacted with,
// keyed by message ID. Populated from the server-rendered DOM on load and
// kept up-to-date as reaction SSE events arrive.
// { [msgId]: Set<emoji> }
export const __myReactions = (() => {
  const map = {};
  document.querySelectorAll('.reactions').forEach((bar) => {
    const msgId = bar.id.replace('reactions-', '');
    bar.querySelectorAll('.reaction-pill--active').forEach((pill) => {
      const emoji = pill.dataset.emoji;
      if (!emoji) return;
      if (!map[msgId]) map[msgId] = new Set();
      map[msgId].add(emoji);
    });
  });
  return map;
})();

// applyMyReactions marks the current user's active emojis on a reaction bar element.
export function applyMyReactions(barEl, msgId) {
  const mine = __myReactions[msgId];
  if (!mine || mine.size === 0) return;
  barEl.querySelectorAll('.reaction-pill').forEach((pill) => {
    if (mine.has(pill.dataset.emoji)) {
      pill.classList.add('reaction-pill--active');
    }
  });
}

// ---- Reaction picker ----
// Clicking the "add reaction" button opens the global emoji picker in reaction
// mode, positioned above the button. Selecting an emoji POSTs the reaction.
let openMsgId = null; // msgId for which the picker is currently open

function closeReactionPicker() {
  const container = document.getElementById('emoji-picker-container');
  if (container && container.dataset.mode === 'reaction') {
    container.hidden = true;
    delete container.dataset.reactionTarget;
    delete container.dataset.mode;
  }
  openMsgId = null;
}

document.addEventListener('click', (e) => {
  const addBtn = e.target.closest('[data-reaction-add]');
  if (addBtn) {
    e.stopPropagation();
    const msgId = addBtn.dataset.reactionAdd;
    const container = document.getElementById('emoji-picker-container');
    if (!container) return;

    // Toggle: clicking the same button again closes the picker.
    if (openMsgId === msgId) {
      closeReactionPicker();
      return;
    }

    // Position the picker above the "+" button (fixed coordinates).
    const rect = addBtn.getBoundingClientRect();
    const pickerWidth = 340;
    let left = rect.left;
    if (left + pickerWidth > window.innerWidth - 8) {
      left = window.innerWidth - pickerWidth - 8;
    }
    container.style.left = `${Math.max(8, left)}px`;
    container.style.right = '';
    container.style.bottom = `${window.innerHeight - rect.top + 6}px`;

    openMsgId = msgId;
    container.dataset.reactionTarget = msgId;
    container.dataset.mode = 'reaction';
    container.hidden = false;
    return;
  }

  // Click outside the picker and add button — close.
  if (!e.target.closest('#emoji-picker-container') && !e.target.closest('[data-reaction-add]')) {
    if (openMsgId !== null) closeReactionPicker();
  }
});

// Wire emoji-click for reaction mode using a document-level capture listener.
// Capture on document fires before any bubble listener (including app/emoji-picker.js) and
// avoids querying emoji-picker before it is parsed into the DOM.
document.addEventListener(
  'emoji-click',
  (ev) => {
    const container = document.getElementById('emoji-picker-container');
    if (!container || container.dataset.mode !== 'reaction') return;
    ev.stopImmediatePropagation();
    const msgId = container.dataset.reactionTarget;
    if (!msgId) return;
    const emoji = ev.detail.unicode;
    closeReactionPicker();
    const body = new URLSearchParams({ emoji });
    fetch(`/rooms/${window.roomID}/messages/${msgId}/reactions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
    });
  },
  { capture: true },
);

// ---- Long-press on reaction pills (touch devices) — show reactor tooltip ----
const LONG_PRESS_MS = 400;
let timer = null;
let activePill = null;

function dismiss() {
  if (activePill) {
    const tip = activePill.querySelector('.reaction-tooltip');
    if (tip) tip.classList.remove('reaction-tooltip--visible');
    activePill = null;
  }
}

document.addEventListener('touchstart', (e) => {
  const pill = e.target.closest('.reaction-pill');
  if (!pill) { dismiss(); return; }

  timer = setTimeout(() => {
    timer = null;
    dismiss();
    activePill = pill;
    const tip = pill.querySelector('.reaction-tooltip');
    if (tip) tip.classList.add('reaction-tooltip--visible');
  }, LONG_PRESS_MS);
}, { passive: true });

document.addEventListener('touchmove', () => {
  if (timer) { clearTimeout(timer); timer = null; }
}, { passive: true });

document.addEventListener('touchend', (e) => {
  if (timer) {
    clearTimeout(timer);
    timer = null;
  } else if (activePill && e.target.closest('.reaction-pill') === activePill) {
    e.preventDefault();
  }
});
