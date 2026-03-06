import '/static/room/edit.js';

// ---- Keyboard-centric navigation ----
// Allows the user to navigate messages with arrow keys, edit with 'e',
// delete with 'd', and return to the textarea with Escape.
const composeTa = document.querySelector('.message-form__textarea');
if (composeTa) {
  // The currently keyboard-active message element, or null.
  let activeMsg = null;

  // Return the ordered list of message articles in the DOM.
  function getMessages() {
    return Array.from(document.querySelectorAll('#message-list-content article.message'));
  }

  // Set the active message, adding/removing the highlight class.
  function setActive(el) {
    if (activeMsg) activeMsg.classList.remove('message--active');
    activeMsg = el || null;
    if (activeMsg) {
      activeMsg.classList.add('message--active');
      activeMsg.scrollIntoView({ block: 'nearest' });
    }
  }

  // Clear navigation state, return focus to the compose textarea, and
  // scroll to the newest message.
  function exitNav() {
    setActive(null);
    composeTa.focus();
    const list = document.getElementById('message-list');
    if (list) list.scrollTop = list.scrollHeight;
  }

  // -- Textarea: ArrowUp on empty value enters navigation mode ------------
  composeTa.addEventListener('keydown', (e) => {
    // Only fire when the emoji autocomplete is hidden (those keys are handled there).
    const acList = document.getElementById('emoji-autocomplete');
    if (acList && !acList.hidden) return;

    if (e.key === 'ArrowUp' && composeTa.value === '') {
      e.preventDefault();
      const msgs = getMessages();
      if (msgs.length === 0) return;
      setActive(msgs[msgs.length - 1]);
      composeTa.blur();
    }
  });

  // -- Global keydown: navigation keys while a message is active ----------
  document.addEventListener('keydown', (e) => {
    // Global Escape: close any open edit form first; then clear nav; then
    // focus textarea (all in one handler — no separate edit-form Escape needed).
    if (e.key === 'Escape') {
      // Check if an edit form is currently open (visible).
      const openForm = document.querySelector('.message-edit-form:not([hidden])');
      if (openForm) {
        e.preventDefault();
        const msgId = openForm.id.replace('edit-form-', '');
        window.__closeEdit(msgId);
        exitNav();
        return;
      }
      // Exit message navigation if active.
      if (activeMsg) {
        e.preventDefault();
        exitNav();
        return;
      }
      // Otherwise just make sure the textarea is focused.
      composeTa.focus();
      return;
    }

    // The rest only applies while in navigation mode.
    if (!activeMsg) return;

    // Ignore key combos with modifier keys (Ctrl, Meta, Alt) except Shift.
    if (e.ctrlKey || e.metaKey || e.altKey) return;

    // Arrow movement: skip if the event originated from the compose textarea
    // (that listener already handled the nav-entry, so we must not move again).
    if (e.key === 'ArrowUp' && e.target !== composeTa) {
      e.preventDefault();
      const msgs = getMessages();
      const idx = msgs.indexOf(activeMsg);
      if (idx > 0) setActive(msgs[idx - 1]);
      return;
    }

    if (e.key === 'ArrowDown' && e.target !== composeTa) {
      e.preventDefault();
      const msgs = getMessages();
      const idx = msgs.indexOf(activeMsg);
      if (idx < msgs.length - 1) {
        setActive(msgs[idx + 1]);
      } else {
        // At the last message — exit nav and return to textarea.
        exitNav();
      }
      return;
    }

    if (e.key === 'e') {
      // Open edit on the active message if the current user owns it.
      const editTrigger = activeMsg.querySelector('[data-edit-trigger]');
      if (editTrigger && !editTrigger.hidden) {
        e.preventDefault();
        const msgId = editTrigger.dataset.editTrigger;
        setActive(null); // remove highlight before entering edit mode
        window.__openEdit(msgId);
      }
      return;
    }

    if (e.key === 'd') {
      // Delete the active message if the current user owns it.
      const deleteBtn = activeMsg.querySelector('.message__delete');
      if (deleteBtn && !deleteBtn.hidden) {
        e.preventDefault();
        if (window.confirm('Delete this message?')) {
          exitNav(); // clear active state before the element is removed
          deleteBtn.click(); // triggers HTMX (optimistic UX + SSE removal)
        }
      }
      return;
    }

    // Any other printable key: exit nav and let the key fall through to the
    // compose textarea so the user can start typing naturally.
    if (e.key.length === 1) {
      exitNav();
      // Don't preventDefault — the keypress will land in the textarea.
    }
  });

  // If an SSE delete removes the currently active message, clear the state.
  document.addEventListener('htmx:sseMessage', () => {
    if (activeMsg && !document.body.contains(activeMsg)) {
      setActive(null);
    }
  });
}
