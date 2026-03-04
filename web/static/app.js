// Theme toggle: persist choice in localStorage, fall back to OS preference.
(() => {
  const root = document.documentElement;
  const stored = localStorage.getItem('theme');
  if (stored) root.setAttribute('data-theme', stored);

  document.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-theme-toggle]');
    if (!btn) return;
    const current = root.getAttribute('data-theme');
    const isDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const next = current === 'dark' ? 'light'
               : current === 'light' ? 'auto'
               : isDark ? 'light' : 'dark';
    root.setAttribute('data-theme', next);
    localStorage.setItem('theme', next);
    btn.setAttribute('aria-label', `Theme: ${next}`);
  });
})();

// Emoji picker toggle + insertion.
(() => {
  const container = document.getElementById('emoji-picker-container');
  document.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-emoji-toggle]');
    if (!btn) return;
    container.hidden = !container.hidden;
    if (!container.hidden) {
      // Position near button.
      const rect = btn.getBoundingClientRect();
      container.style.position = 'fixed';
      container.style.bottom = `${window.innerHeight - rect.top + 8}px`;
      container.style.left = `${rect.left}px`;
      container.style.zIndex = 999;
    }
  });

  document.addEventListener('emoji-click', (e) => {
    // Reaction mode is handled entirely by room.js; skip textarea insertion.
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
  // Exclude the reaction-add button: room.js handles open/close for that.
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
})();

// Format message timestamps in the user's local timezone.
(() => {
  function formatMessageTimes() {
    const now = new Date();
    const yesterday = new Date(now);
    yesterday.setDate(now.getDate() - 1);

    document.querySelectorAll('time.message__time').forEach((el) => {
      const dt = new Date(el.getAttribute('datetime'));
      if (Number.isNaN(dt)) return;

      const timeStr = dt.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

      if (dt.toDateString() === now.toDateString()) {
        el.textContent = timeStr;
      } else if (dt.toDateString() === yesterday.toDateString()) {
        el.textContent = `Yesterday at ${timeStr}`;
      } else if (dt.getFullYear() === now.getFullYear()) {
        el.textContent =
          dt.toLocaleDateString([], { weekday: 'short', day: 'numeric', month: 'short' }) +
          ' at ' +
          timeStr;
      } else {
        el.textContent =
          dt.toLocaleDateString([], {
            weekday: 'short',
            day: 'numeric',
            month: 'short',
            year: 'numeric',
          }) +
          ' at ' +
          timeStr;
      }
    });
  }

  document.addEventListener('DOMContentLoaded', formatMessageTimes);
  document.addEventListener('htmx:afterSettle', formatMessageTimes);
})();

// Lightbox for image attachments.
(() => {
  const lightbox = document.getElementById('lightbox');
  if (!lightbox) return;
  const lightboxImg = lightbox.querySelector('.lightbox__img');
  const lightboxClose = lightbox.querySelector('.lightbox__close');

  document.addEventListener('click', (e) => {
    const img = e.target.closest('.message__media-img');
    if (!img) return;
    e.preventDefault();
    lightboxImg.src = img.src;
    lightboxImg.alt = img.alt;
    lightbox.showModal();
  });

  lightboxClose.addEventListener('click', () => lightbox.close());

  lightbox.addEventListener('click', (e) => {
    if (e.target === lightbox) lightbox.close();
  });
})();

// Click-to-copy for code blocks.
(() => {
  document.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-copy-code]');
    if (!btn) return;
    const block = btn.closest('.code-block');
    if (!block) return;
    const raw = block.querySelector('.code-block__raw');
    const text = raw ? raw.value : (block.querySelector('code') || block).textContent;
    navigator.clipboard.writeText(text).then(() => {
      btn.classList.add('code-block__copy--copied');
      const orig = btn.innerHTML;
      btn.innerHTML =
        '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="20 6 9 17 4 12"/></svg>';
      setTimeout(() => {
        btn.classList.remove('code-block__copy--copied');
        btn.innerHTML = orig;
      }, 2000);
    });
  });
})();
