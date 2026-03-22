// Mobile action sheet for message actions (react, copy, edit, delete).
// Triggered by tapping a message on touch devices (hover: none).

const isTouchDevice = matchMedia('(hover: none)').matches &&
  ('ontouchstart' in window || navigator.maxTouchPoints > 0);
if (!isTouchDevice) {
  // No-op on desktop — hover controls remain.
} else {
  // Signal CSS to hide inline edit/delete/reaction-add buttons.
  document.body.dataset.actionSheet = '';

  const dialog = document.getElementById('message-actions');
  const content = dialog.querySelector('.action-sheet__content');
  const btnReact = dialog.querySelector('[data-action="react"]');
  const btnCopy = dialog.querySelector('[data-action="copy"]');
  const btnEdit = dialog.querySelector('[data-action="edit"]');
  const btnDelete = dialog.querySelector('[data-action="delete"]');
  const separator = dialog.querySelector('.action-sheet__separator');
  const preview = document.getElementById('action-sheet-preview');
  const pickerSlot = document.getElementById('action-sheet-picker');
  const actionButtons = dialog.querySelectorAll(
    '.action-sheet__item:not([data-action="react"]), .action-sheet__separator',
  );

  let targetMsgId = null;
  let pickerOpen = false;

  // ---- Excluded elements: taps on these should not open the sheet ----
  const EXCLUDED = 'a, button, img.message__media-img, video, .reaction-pill, .reaction-add, .message-edit-form, input, textarea, .emoji-autocomplete, .mention-autocomplete';

  // ---- Tap detection (delegated click) ----
  document.addEventListener('click', (e) => {
    if (e.target.closest(EXCLUDED)) return;
    const article = e.target.closest('article.message:not(.message--system)');
    if (!article) return;
    // Don't open if an edit form is currently visible on this message.
    const editForm = article.querySelector('.message-edit-form:not([hidden])');
    if (editForm) return;

    openSheet(article);
  });

  function clearHighlight() {
    const prev = document.querySelector('.message--sheet-target');
    if (prev) prev.classList.remove('message--sheet-target');
  }

  function openSheet(article) {
    targetMsgId = article.id.replace('msg-', '');
    const isOwner = article.dataset.authorId === window.__currentUserID;

    // Show/hide owner actions
    btnEdit.hidden = !isOwner;
    btnDelete.hidden = !isOwner;
    separator.hidden = !isOwner;

    // Show/hide copy based on text element existence
    const textEl = document.getElementById(`text-${targetMsgId}`);
    btnCopy.hidden = !textEl;

    // Clone the message into the sheet preview
    const clone = article.cloneNode(true);
    clone.removeAttribute('id');
    clone.classList.remove('message--continuation', 'message--sheet-target');
    // Strip interactive/irrelevant elements from the clone
    for (const el of clone.querySelectorAll(
      '.message__toolbar, .message-edit-form, .reactions, [id^="preview-"], .message__attachments, .message__time'
    )) el.remove();
    // Remove all IDs to avoid duplicates
    for (const el of clone.querySelectorAll('[id]')) el.removeAttribute('id');
    preview.innerHTML = '';
    preview.appendChild(clone);

    // Highlight the tapped message
    clearHighlight();
    article.classList.add('message--sheet-target');

    // Hide content off-screen before opening so there's no flash.
    content.style.transform = 'translateY(100%)';
    dialog.showModal();
    // Wait one frame for the dialog to be fully laid out in the top layer,
    // then trigger the slide-up animation.
    requestAnimationFrame(() => {
      content.style.transform = '';
      dialog.classList.add('is-opening');
      content.addEventListener('animationend', function handler() {
        content.removeEventListener('animationend', handler);
        dialog.classList.remove('is-opening');
      });
    });
  }

  function showInlinePicker() {
    const container = document.getElementById('emoji-picker-container');
    if (!container) return;

    // Configure for reaction mode.
    container.dataset.reactionTarget = targetMsgId;
    container.dataset.mode = 'reaction';

    // Reset any fixed positioning from desktop mode.
    container.style.cssText = '';

    // Move picker into the sheet slot and reveal it.
    pickerSlot.appendChild(container);
    container.hidden = false;
    pickerSlot.hidden = false;
    pickerOpen = true;

    // Hide the other action buttons.
    for (const el of actionButtons) el.style.display = 'none';
  }

  function hideInlinePicker() {
    if (!pickerOpen) return;
    const container = document.getElementById('emoji-picker-container');
    if (container) {
      container.hidden = true;
      delete container.dataset.reactionTarget;
      delete container.dataset.mode;
      // Move picker back to its original home (body-level).
      document.body.appendChild(container);
    }
    pickerSlot.hidden = true;
    pickerOpen = false;

    // Restore action buttons.
    for (const el of actionButtons) el.style.display = '';
  }

  // ---- Actions ----
  btnReact.addEventListener('click', (e) => {
    // Stop propagation so emoji-picker.js's outside-click handler
    // doesn't immediately re-hide the picker we're about to show.
    e.stopPropagation();
    if (pickerOpen) {
      hideInlinePicker();
    } else {
      showInlinePicker();
    }
  });

  btnCopy.addEventListener('click', () => {
    const textEl = document.getElementById(`text-${targetMsgId}`);
    if (textEl) navigator.clipboard.writeText(textEl.innerText);
    closeSheet();
  });

  btnEdit.addEventListener('click', () => {
    const msgId = targetMsgId;
    closeSheet(() => {
      if (window.__openEdit) window.__openEdit(msgId);
    });
  });

  btnDelete.addEventListener('click', () => {
    const msgId = targetMsgId;
    closeSheet(() => {
      const article = document.getElementById(`msg-${msgId}`);
      if (!article) return;
      const deleteBtn = article.querySelector('.message__delete');
      if (deleteBtn) deleteBtn.click();
    });
  });

  // ---- Close with slide-down animation ----
  function closeSheet(afterClose) {
    hideInlinePicker();
    dialog.classList.add('is-closing');
    dialog.addEventListener('animationend', function handler() {
      dialog.removeEventListener('animationend', handler);
      dialog.classList.remove('is-closing');
      dialog.close();
      clearHighlight();
      targetMsgId = null;
      if (afterClose) afterClose();
    });
  }

  // Close the sheet after an emoji is selected in inline reaction mode.
  // reactions.js calls stopImmediatePropagation() on emoji-click, so we
  // observe the picker container's hidden attribute instead.
  const pickerContainer = document.getElementById('emoji-picker-container');
  if (pickerContainer) {
    new MutationObserver(() => {
      if (pickerOpen && pickerContainer.hidden) closeSheet();
    }).observe(pickerContainer, { attributes: true, attributeFilter: ['hidden'] });
  }

  // ---- Backdrop click ----
  dialog.addEventListener('click', (e) => {
    if (e.target === dialog) closeSheet();
  });

  // ---- Escape key (cancel event) ----
  dialog.addEventListener('cancel', (e) => {
    e.preventDefault();
    closeSheet();
  });

  // ---- Swipe-down-to-dismiss (anywhere except emoji picker scroll) ----
  let startY = 0;
  let currentY = 0;
  let dragging = false;
  const DISMISS_THRESHOLD = 80;

  function isInsideEmojiPicker(target) {
    return !!target.closest('emoji-picker');
  }

  dialog.addEventListener('touchstart', (e) => {
    if (isInsideEmojiPicker(e.target)) return;
    dragging = true;
    startY = e.touches[0].clientY;
    currentY = 0;
    content.style.transition = 'none';
  }, { passive: true });

  dialog.addEventListener('touchmove', (e) => {
    if (!dragging) return;
    const dy = Math.max(0, e.touches[0].clientY - startY);
    currentY = dy;
    if (dy > 0) e.preventDefault();
    content.style.transform = `translateY(${dy}px)`;
  }, { passive: false });

  dialog.addEventListener('touchend', () => {
    if (!dragging) return;
    dragging = false;
    content.style.transition = '';
    content.style.transform = '';
    if (currentY > DISMISS_THRESHOLD) {
      closeSheet();
    }
  });

  dialog.addEventListener('touchcancel', () => {
    if (!dragging) return;
    dragging = false;
    content.style.transition = '';
    content.style.transform = '';
  });

  // ---- Graceful close if target message is removed (SSE delete) ----
  const observer = new MutationObserver(() => {
    if (!targetMsgId || !dialog.open) return;
    if (!document.getElementById(`msg-${targetMsgId}`)) {
      hideInlinePicker();
      dialog.close();
      dialog.classList.remove('is-closing');
      clearHighlight();
      targetMsgId = null;
    }
  });
  observer.observe(document.getElementById('message-list-content'), { childList: true });
}
