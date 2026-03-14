// Persist compose-textarea drafts in localStorage, scoped per room.
const ta = document.querySelector('.message-form__textarea');
const key = `draft:${window.roomID}`;

if (ta && window.roomID) {
  // Restore draft on load.
  const saved = localStorage.getItem(key);
  if (saved) {
    ta.value = saved;
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  }

  // Save on every keystroke (localStorage is synchronous and fast).
  ta.addEventListener('input', () => {
    if (ta.value) localStorage.setItem(key, ta.value);
    else localStorage.removeItem(key);
  });

  // Clear draft after successful send (form reset).
  ta.closest('form').addEventListener('reset', () => localStorage.removeItem(key));
}
