import { Database } from 'https://cdn.jsdelivr.net/npm/emoji-picker-element/+esm';
// Expose Database so room-page module scripts can instantiate it without
// re-importing the same URL (which would be a separate module instance).
window.__EmojiDatabase = Database;

const container = document.getElementById('emoji-picker-container');

document.addEventListener('click', (e) => {
  const btn = e.target.closest('[data-emoji-toggle]');
  if (!btn) return;
  container.hidden = !container.hidden;
  if (!container.hidden) {
    const rect = btn.getBoundingClientRect();
    container.style.position = 'fixed';
    container.style.bottom = `${window.innerHeight - rect.top + 8}px`;
    container.style.left = `${rect.left}px`;
    container.style.zIndex = 999;
  }
});

document.addEventListener('emoji-click', (e) => {
  // Reaction mode is handled entirely by room/reactions.js; skip textarea insertion.
  if (container.dataset.mode === 'reaction') return;
  const textarea = document.querySelector('.message-form__textarea');
  if (textarea) {
    const pos = textarea.selectionStart ?? textarea.value.length;
    const before = textarea.value.slice(0, pos);
    const after = textarea.value.slice(pos);
    textarea.value = before + e.detail.unicode + after;
    textarea.focus();
    textarea.selectionStart = textarea.selectionEnd = pos + e.detail.unicode.length;
  }
  container.hidden = true;
});

// Close picker on outside click.
// Exclude the reaction-add button: room/reactions.js handles open/close for that.
document.addEventListener('click', (e) => {
  if (
    !container.hidden &&
    !e.target.closest('#emoji-picker-container') &&
    !e.target.closest('[data-emoji-toggle]') &&
    !e.target.closest('[data-reaction-add]')
  ) {
    container.hidden = true;
  }
});
