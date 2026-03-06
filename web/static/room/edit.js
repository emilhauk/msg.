// ---- Optimistic delete UX ----
// On delete request: dim the message and replace the trash icon with a
// spinner so the user has clear feedback and can't double-submit.
// On failure: restore everything so the user can retry.
const SPINNER_HTML = '<span class="attachment-chip__spinner" aria-hidden="true"></span>';

// Saved button contents keyed by the button element itself (WeakMap so
// there's no need to manually clean up after the element is removed).
const savedHTML = new WeakMap();

function onDeleteBefore(e) {
  const btn = e.detail.elt;
  if (!btn || !btn.classList.contains('message__delete')) return;
  const article = btn.closest('article.message');
  if (!article) return;

  article.classList.add('message--deleting');
  savedHTML.set(btn, btn.innerHTML);
  btn.innerHTML = SPINNER_HTML;
  btn.disabled = true;
}

function onDeleteError(e) {
  const btn = e.detail.elt;
  if (!btn || !btn.classList.contains('message__delete')) return;
  const article = btn.closest('article.message');
  if (article) article.classList.remove('message--deleting');

  const html = savedHTML.get(btn);
  if (html !== undefined) btn.innerHTML = html;
  btn.disabled = false;
}

document.addEventListener('htmx:beforeRequest', onDeleteBefore);
document.addEventListener('htmx:responseError', onDeleteError);
document.addEventListener('htmx:sendError', onDeleteError);

// ---- Inline message edit UX ----
// Uses event delegation so it works on messages inserted via SSE.
export function openEdit(msgId) {
  const article = document.getElementById(`msg-${msgId}`);
  if (!article) return;
  const textEl = document.getElementById(`text-${msgId}`);
  const form = document.getElementById(`edit-form-${msgId}`);
  if (!textEl || !form) return;
  textEl.hidden = true;
  form.hidden = false;
  // Auto-size the textarea to its content.
  const ta = form.querySelector('textarea');
  if (ta) {
    ta.style.height = 'auto';
    ta.style.height = `${Math.min(ta.scrollHeight, 300)}px`;
    ta.focus();
    // Move cursor to end.
    ta.selectionStart = ta.selectionEnd = ta.value.length;
  }
}

export function closeEdit(msgId) {
  const article = document.getElementById(`msg-${msgId}`);
  if (!article) return;
  const textEl = document.getElementById(`text-${msgId}`);
  const form = document.getElementById(`edit-form-${msgId}`);
  if (textEl) textEl.hidden = false;
  if (form) form.hidden = true;
}

// Expose via window so browser tests and keyboard.js can intercept/call them.
window.__openEdit = openEdit;
window.__closeEdit = closeEdit;

// Click delegation — edit trigger and cancel buttons.
document.addEventListener('click', (e) => {
  const trigger = e.target.closest('[data-edit-trigger]');
  if (trigger) {
    openEdit(trigger.dataset.editTrigger);
    return;
  }
  const cancel = e.target.closest('[data-edit-cancel]');
  if (cancel) {
    closeEdit(cancel.dataset.editCancel);
  }
});

// Enter (without Shift) submits the edit form; Shift+Enter inserts newline.
document.addEventListener('keydown', (e) => {
  if (e.key !== 'Enter' || e.shiftKey) return;
  const ta = e.target;
  if (!ta || !ta.classList.contains('message-edit-form__textarea')) return;
  const form = ta.closest('.message-edit-form');
  if (!form) return;
  e.preventDefault();
  form.requestSubmit();
});

// Auto-resize edit textarea on input.
document.addEventListener('input', (e) => {
  const ta = e.target;
  if (!ta || !ta.classList.contains('message-edit-form__textarea')) return;
  ta.style.height = 'auto';
  ta.style.height = `${Math.min(ta.scrollHeight, 300)}px`;
});

// Close the edit form immediately on a successful PATCH (204).
// The SSE edit event will replace the full article shortly after, but
// closing optimistically means the user sees instant feedback.
document.addEventListener('htmx:afterRequest', (e) => {
  const form = e.detail.elt;
  if (!form || !form.classList.contains('message-edit-form')) return;
  if (e.detail.successful) {
    const msgId = form.id.replace('edit-form-', '');
    closeEdit(msgId);
    const ta = document.querySelector('.message-form__textarea');
    if (ta) ta.focus();
  }
});
