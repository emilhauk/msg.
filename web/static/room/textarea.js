import { replaceAtCursor, replaceAllEmoticons } from '/static/room/emoticons.js';

// Auto-resize textarea; reset height when the form is cleared.
const ta = document.querySelector('.message-form__textarea');
if (ta) {
  ta.addEventListener('input', function () {
    this.style.height = 'auto';
    this.style.height = `${Math.min(this.scrollHeight, 140)}px`;
  });
  ta.closest('form').addEventListener('reset', () => {
    ta.style.height = '';
  });
}

// Main textarea: live emoticon replacement on input + full pass before submit.
const form = document.querySelector('.message-form');
if (ta && form) {
  ta.addEventListener('input', function () {
    replaceAtCursor(this);
  });

  // Full-text pass on submit so a trailing emoticon (no space before Enter)
  // is always converted even if the live handler didn't fire for it.
  form.addEventListener('submit', () => {
    ta.value = replaceAllEmoticons(ta.value);
  });
}

// Edit textareas: use event delegation (they are created dynamically via SSE).
document.addEventListener('input', (e) => {
  const t = e.target;
  if (t?.classList.contains('message-edit-form__textarea')) {
    replaceAtCursor(t);
  }
});
document.addEventListener('submit', (e) => {
  const editForm = e.target;
  if (!editForm || !editForm.classList.contains('message-edit-form')) return;
  const t = editForm.querySelector('.message-edit-form__textarea');
  if (t) t.value = replaceAllEmoticons(t.value);
});
