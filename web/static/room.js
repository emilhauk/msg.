// applyOwnerControls unhides the edit and delete buttons on a message article
// when the current user is the author. Called at page load and after every
// SSE message insert or edit replacement so buttons are never baked into the
// shared SSE broadcast HTML.
function applyOwnerControls(articleEl) {
  if (!articleEl || articleEl.dataset.authorId !== __currentUserID) return;
  const editBtn = articleEl.querySelector('.message__edit');
  const deleteBtn = articleEl.querySelector('.message__delete');
  if (editBtn) editBtn.removeAttribute('hidden');
  if (deleteBtn) deleteBtn.removeAttribute('hidden');
}

// Apply on all messages already in the DOM at page load.
document.querySelectorAll('#message-list-content article.message').forEach(applyOwnerControls);

// Apply on messages loaded via infinite-scroll history swap.
document.body.addEventListener('htmx:afterSwap', (e) => {
  // The sentinel swaps history HTML beforebegin itself; the inserted nodes
  // are siblings. We re-scan the whole list to catch all newly added articles.
  if (e.detail.elt?.classList?.contains('scroll-sentinel')) {
    document.querySelectorAll('#message-list-content article.message').forEach(applyOwnerControls);
    document.getElementById('history-spinner')?.classList.remove('is-loading');
  }
});

// Show spinner when the scroll-sentinel fires an HTMX history request.
document.body.addEventListener('htmx:beforeRequest', (e) => {
  if (e.detail.elt?.classList?.contains('scroll-sentinel')) {
    document.getElementById('history-spinner')?.classList.add('is-loading');
  }
});

// Hide spinner on network/response errors from the sentinel.
document.body.addEventListener('htmx:responseError', (e) => {
  if (e.detail.elt?.classList?.contains('scroll-sentinel')) {
    document.getElementById('history-spinner')?.classList.remove('is-loading');
  }
});
document.body.addEventListener('htmx:sendError', (e) => {
  if (e.detail.elt?.classList?.contains('scroll-sentinel')) {
    document.getElementById('history-spinner')?.classList.remove('is-loading');
  }
});

// ---- Scroll management ----
// userScrolledUp is set to true only by an explicit user scroll gesture
// that moves more than half a viewport from the bottom. This decouples
// "user deliberately scrolled up" from "there happens to be distance from
// the bottom right now" (which is always true while images are still loading).
(() => {
  const list = document.getElementById('message-list');
  const content = document.getElementById('message-list-content');
  if (!list) return;

  let userScrolledUp = false;

  // Snap to bottom instantly (no smooth animation so repeated ResizeObserver
  // callbacks can't race with an in-progress smooth scroll).
  function snapToBottom() {
    list.scrollTop = list.scrollHeight;
  }

  function snapUnlessScrolledUp() {
    if (!userScrolledUp) snapToBottom();
  }

  // Track deliberate user scrolling. Only flip the flag on actual scroll
  // events — not on programmatic scrollTop assignments.
  list.addEventListener(
    'scroll',
    () => {
      const dist = list.scrollHeight - list.scrollTop - list.clientHeight;
      const threshold = list.clientHeight / 2;
      if (dist >= threshold) {
        userScrolledUp = true;
      } else {
        // Scrolled back close to the bottom — resume auto-scroll.
        userScrolledUp = false;
      }
    },
    { passive: true },
  );

  // Initial snap on page load.
  snapToBottom();

  // Focus the compose textarea so the user can start typing immediately.
  const initTa = document.querySelector('.message-form__textarea');
  if (initTa && window.matchMedia('(hover: hover) and (pointer: fine)').matches) initTa.focus();

  // ResizeObserver on the inner content wrapper fires whenever images,
  // videos, or unfurl cards load and expand the layout — which is invisible
  // to an observer on the scrollable container itself.
  if (content && typeof ResizeObserver !== 'undefined') {
    new ResizeObserver(snapUnlessScrolledUp).observe(content);
  }

  // Fast path for text-only SSE messages: they insert a new DOM node but
  // produce no resize event on the content wrapper until the node is painted.
  // Also apply owner controls on the newly inserted message article.
  document.body.addEventListener('htmx:sseMessage', () => {
    snapUnlessScrolledUp();
    // The newly inserted article is the one immediately before #sse-message-target.
    const target = document.getElementById('sse-message-target');
    if (target?.previousElementSibling) {
      applyOwnerControls(target.previousElementSibling);
    }
  });
})();

// Auto-resize textarea; reset height when the form is cleared.
(() => {
  const ta = document.querySelector('.message-form__textarea');
  if (!ta) return;
  ta.addEventListener('input', function () {
    this.style.height = 'auto';
    this.style.height = `${Math.min(this.scrollHeight, 140)}px`;
  });
  ta.closest('form').addEventListener('reset', () => {
    ta.style.height = '';
  });
})();

// ---- ASCII emoticon → emoji replacement ----
// Replaces common ASCII emoticons (e.g. :) :( <3) with their unicode emoji
// equivalents. Replacement fires on word boundaries (space / newline) while
// typing, and also as a full-text pass just before the form is submitted so
// that trailing emoticons (e.g. "hello :)" + Enter) are always converted.
(() => {
  // Map of ASCII emoticon → unicode emoji.  Order matters for the regex:
  // longer / more specific patterns must come before shorter ones that share
  // a prefix (e.g. ">:-(" before ">:(", ":'(" before ":(").
  const EMOTICONS = {
    '>:-(': '😠',
    '>:(': '😠',
    ":'-(": '😢',
    ":'(": '😢',
    'O:-)': '😇',
    'O:)': '😇',
    ':-)': '😊',
    ':)': '😊',
    ':^)': '😊',
    ':-D': '😄',
    ':D': '😄',
    XD: '😆',
    xD: '😆',
    ':-(': '😞',
    ':(': '😞',
    ':-P': '😛',
    ':P': '😛',
    ':-p': '😛',
    ':p': '😛',
    ';-)': '😉',
    ';)': '😉',
    ':-O': '😮',
    ':O': '😮',
    ':-o': '😮',
    ':o': '😮',
    ':-*': '😘',
    ':*': '😘',
    ':-|': '😐',
    ':|': '😐',
    '8-)': '😎',
    '8)': '😎',
    'B-)': '😎',
    'B)': '😎',
    ':-x': '😶',
    ':x': '😶',
    ':3': '🥺',
    '</3': '💔',
    '<3': '❤️',
  };

  // Build a regex that matches any emoticon preceded by a word boundary
  // (start of string, whitespace, or another emoticon terminator).
  // Each emoticon is regex-escaped so characters like ( ) | are literal.
  function escapeRe(s) {
    return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  }
  const pattern = new RegExp(
    `(?:^|(?<=\\s))(${Object.keys(EMOTICONS).map(escapeRe).join('|')})(?=\\s|$)`,
    'g',
  );

  // Replace all emoticons in a plain string (used for the pre-submit full pass).
  function replaceAllEmoticons(text) {
    return text.replace(pattern, (m) => EMOTICONS[m] || m);
  }

  // Replace the emoticon immediately to the left of the cursor, if the
  // character just typed is a word boundary (space or newline).
  // Returns true if a replacement was made so callers can skip other logic.
  function replaceAtCursor(ta) {
    const pos = ta.selectionStart;
    const val = ta.value;
    // Only act when the character before the cursor is a word boundary.
    const trigger = val[pos - 1];
    if (trigger !== ' ' && trigger !== '\n') return false;

    // Scan backwards from just before the boundary to find a candidate.
    // We only need to look back as far as the longest emoticon (5 chars).
    const MAX = 6;
    const searchStart = Math.max(0, pos - 1 - MAX);
    const segment = val.slice(searchStart, pos - 1); // text before the space/newline

    // Try each emoticon, longest first (already ordered above).
    for (const emoticon in EMOTICONS) {
      if (segment.endsWith(emoticon)) {
        // Check that the emoticon is at a word boundary on the left too.
        const emStart = pos - 1 - emoticon.length;
        if (emStart > 0) {
          const charBefore = val[emStart - 1];
          if (charBefore !== ' ' && charBefore !== '\n') continue;
        }
        const emoji = EMOTICONS[emoticon];
        ta.value = val.slice(0, pos - 1 - emoticon.length) + emoji + val.slice(pos - 1);
        // Reposition cursor after the inserted emoji + the boundary char.
        const newPos = pos - emoticon.length + emoji.length;
        ta.selectionStart = ta.selectionEnd = newPos;
        return true;
      }
    }
    return false;
  }

  // Main textarea: live replacement on input + full pass before submit.
  const form = document.querySelector('.message-form');
  const ta = form?.querySelector('.message-form__textarea');
  if (ta) {
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
})();

// ---- Media upload (paste + drag-and-drop) ----
// Shared upload logic for both paste and drag-drop. Uploads files directly
// to S3 via a presigned PUT URL and queues them as attachment chips on the form.
(() => {
  const ALLOWED_TYPES = {
    'image/jpeg': true,
    'image/png': true,
    'image/gif': true,
    'image/webp': true,
    'video/mp4': true,
    'video/webm': true,
  };
  const ta = document.querySelector('.message-form__textarea');
  const form = document.querySelector('.message-form');
  const previewsEl = document.getElementById('attachment-previews');
  const inputEl = document.getElementById('attachment-input');
  const fileInput = document.getElementById('file-input');
  if (!ta || !form || !previewsEl || !inputEl) return;

  let pendingAttachments = []; // [{url, content_type, filename}]
  let uploadCount = 0; // number of uploads currently in-flight

  function syncInput() {
    inputEl.value = pendingAttachments.length ? JSON.stringify(pendingAttachments) : '';
  }

  function setSendDisabled(disabled) {
    const btn = form.querySelector('.message-form__send');
    if (btn) btn.disabled = disabled;
  }

  // Generate a 12-character random hex string to use as the file's key stem.
  function randomHex(len) {
    const arr = new Uint8Array(Math.ceil(len / 2));
    crypto.getRandomValues(arr);
    return Array.from(arr, (b) => b.toString(16).padStart(2, '0'))
      .join('')
      .slice(0, len);
  }

  // Build a preview chip element for a pending upload.
  // Shows a thumbnail (images) or a video icon; no filename label.
  function makeChip(contentType, objectURL) {
    const chip = document.createElement('div');
    chip.className = 'attachment-chip';

    if (contentType.startsWith('image/')) {
      const thumb = document.createElement('img');
      thumb.src = objectURL;
      thumb.alt = '';
      chip.appendChild(thumb);
    } else {
      const icon = document.createElement('span');
      icon.className = 'attachment-chip__icon';
      icon.textContent = '\uD83C\uDFA5'; // 🎥
      chip.appendChild(icon);
    }

    const spinner = document.createElement('span');
    spinner.className = 'attachment-chip__spinner';
    chip.appendChild(spinner);

    const removeBtn = document.createElement('button');
    removeBtn.type = 'button';
    removeBtn.className = 'attachment-chip__remove';
    removeBtn.setAttribute('aria-label', 'Remove attachment');
    removeBtn.textContent = '\u00D7'; // ×
    removeBtn.addEventListener('click', () => {
      const idx = parseInt(chip.dataset.attachmentIdx, 10);
      if (!Number.isNaN(idx)) {
        pendingAttachments.splice(idx, 1);
        // Re-index remaining chips.
        const chips = previewsEl.querySelectorAll('.attachment-chip[data-attachment-idx]');
        let counter = 0;
        chips.forEach((c) => {
          c.dataset.attachmentIdx = counter++;
        });
        syncInput();
      }
      chip.remove();
      if (previewsEl.children.length === 0) previewsEl.hidden = true;
    });
    chip.appendChild(removeBtn);

    previewsEl.hidden = false;
    previewsEl.appendChild(chip);
    return chip;
  }

  // Upload a single File object: presign → PUT → chip.
  // Shared by paste, drag-drop, and file-picker handlers.
  function uploadFile(file) {
    const objectURL = URL.createObjectURL(file);
    const chip = makeChip(file.type, objectURL);

    uploadCount++;
    setSendDisabled(true);

    const hash = randomHex(12);
    const params = new URLSearchParams({
      hash: hash,
      content_type: file.type,
      content_length: file.size,
    });

    fetch(`/rooms/${roomID}/upload-url?${params}`, { credentials: 'same-origin' })
      .then((r) => {
        if (!r.ok) throw new Error(`presign failed: ${r.status}`);
        return r.json();
      })
      .then((data) =>
        fetch(data.upload_url, {
          method: 'PUT',
          headers: { 'Content-Type': file.type },
          body: file,
        }).then((r) => {
          if (!r.ok) throw new Error(`upload failed: ${r.status}`);
          return data.public_url;
        }),
      )
      .then((publicURL) => {
        const idx = pendingAttachments.length;
        pendingAttachments.push({ url: publicURL, content_type: file.type, filename: hash });
        chip.dataset.attachmentIdx = idx;
        syncInput();
        chip.classList.add('attachment-chip--done');
        chip.querySelector('.attachment-chip__spinner').remove();
      })
      .catch(() => {
        chip.classList.add('attachment-chip--error');
        const spinner = chip.querySelector('.attachment-chip__spinner');
        if (spinner) spinner.remove();
      })
      .finally(() => {
        URL.revokeObjectURL(objectURL);
        uploadCount--;
        if (uploadCount === 0) setSendDisabled(false);
      });
  }

  // ---- File picker button ----
  // Clicking the paperclip button opens the native OS / mobile file picker.
  const attachBtn = document.querySelector('[data-attach-trigger]');
  if (attachBtn && fileInput) {
    attachBtn.addEventListener('click', () => {
      fileInput.click();
    });
    fileInput.addEventListener('change', () => {
      Array.from(fileInput.files || []).forEach((file) => {
        if (ALLOWED_TYPES[file.type]) uploadFile(file);
      });
      // Reset so selecting the same file again triggers another change event.
      fileInput.value = '';
    });
  }

  // ---- Paste handler ----
  ta.addEventListener('paste', (e) => {
    const items = Array.from(e.clipboardData?.items || []);
    const mediaItems = items.filter((i) => i.kind === 'file' && ALLOWED_TYPES[i.type]);
    if (mediaItems.length === 0) return;

    // Prevent the browser pasting binary data as text into the textarea.
    e.preventDefault();

    mediaItems.forEach((item) => {
      const file = item.getAsFile();
      if (!file) return;
      uploadFile(file);
    });
  });

  // ---- Drag-and-drop handler ----
  // Target: the full .room-main column (message list + compose area).
  (() => {
    const roomMain = document.querySelector('.room-main');
    const overlay = document.getElementById('drop-overlay');
    if (!roomMain || !overlay) return;

    // Track enter/leave depth to avoid flicker when crossing child elements.
    let dragDepth = 0;

    function hasDragFiles(e) {
      const types = e.dataTransfer?.types;
      if (!types) return false;
      // types is a DOMStringList or Array depending on browser.
      return Array.prototype.indexOf.call(types, 'Files') >= 0;
    }

    function showOverlay() {
      overlay.removeAttribute('hidden');
      // rAF so the hidden→visible transition plays.
      requestAnimationFrame(() => {
        overlay.classList.add('drop-overlay--active');
      });
    }

    function hideOverlay() {
      overlay.classList.remove('drop-overlay--active');
      overlay.setAttribute('hidden', '');
      dragDepth = 0;
    }

    roomMain.addEventListener('dragenter', (e) => {
      if (!hasDragFiles(e)) return;
      e.preventDefault();
      dragDepth++;
      if (dragDepth === 1) showOverlay();
    });

    roomMain.addEventListener('dragover', (e) => {
      if (!hasDragFiles(e)) return;
      e.preventDefault();
      // Signal to the OS that we accept a copy (not a move).
      e.dataTransfer.dropEffect = 'copy';
    });

    roomMain.addEventListener('dragleave', () => {
      dragDepth--;
      if (dragDepth <= 0) hideOverlay();
    });

    roomMain.addEventListener('drop', (e) => {
      e.preventDefault();
      hideOverlay();

      const files = Array.from(e.dataTransfer?.files || []);
      files.forEach((file) => {
        if (ALLOWED_TYPES[file.type]) uploadFile(file);
      });

      // Focus the textarea so the user can type a caption immediately.
      if (ta) ta.focus();
    });
  })();

  // Clear attachments when the form resets (fires after successful HTMX send).
  form.addEventListener('reset', () => {
    pendingAttachments = [];
    syncInput();
    previewsEl.innerHTML = '';
    previewsEl.hidden = true;
    setSendDisabled(false);
    uploadCount = 0;
  });
})();

// ---- Emoji shortcode autocomplete ----
// Waits for the base.html module to expose window.__EmojiDatabase, then
// wires up :shortcode → dropdown → insert behaviour on the message textarea.
(() => {
  // Poll until the ESM module has run (usually <50 ms after DOMContentLoaded).
  function waitForDb(cb) {
    if (window.__EmojiDatabase) {
      cb(new window.__EmojiDatabase());
      return;
    }
    const t = setInterval(() => {
      if (window.__EmojiDatabase) {
        clearInterval(t);
        cb(new window.__EmojiDatabase());
      }
    }, 50);
  }

  waitForDb((db) => {
    const ta = document.querySelector('.message-form__textarea');
    const list = document.getElementById('emoji-autocomplete');
    if (!ta || !list) return;

    let activeIdx = -1; // currently highlighted item index
    let matchStart = -1; // index of the leading ':' in textarea.value
    let matchEnd = -1; // index one past the last typed char

    // ------------------------------------------------------------------
    // Helpers
    // ------------------------------------------------------------------

    function getItems() {
      return list.querySelectorAll('.emoji-autocomplete__item');
    }

    function highlight(idx) {
      const items = getItems();
      items.forEach((el, i) => {
        el.setAttribute('aria-selected', i === idx ? 'true' : 'false');
      });
      activeIdx = idx;
    }

    function hideDropdown() {
      list.hidden = true;
      list.innerHTML = '';
      activeIdx = -1;
      matchStart = -1;
      matchEnd = -1;
    }

    function positionDropdown() {
      const rect = ta.getBoundingClientRect();
      list.style.left = `${rect.left}px`;
      list.style.bottom = `${window.innerHeight - rect.top + 4}px`;
      list.style.width = `${rect.width}px`;
    }

    function insertEmoji(unicode) {
      if (matchStart < 0) return;
      const before = ta.value.slice(0, matchStart);
      const after = ta.value.slice(matchEnd);
      ta.value = before + unicode + after;
      // Place cursor right after the inserted character.
      const pos = matchStart + unicode.length;
      ta.selectionStart = ta.selectionEnd = pos;
      // Trigger auto-resize.
      ta.dispatchEvent(new Event('input'));
      ta.focus();
      hideDropdown();
    }

    // ------------------------------------------------------------------
    // Find the ':word' fragment immediately behind the cursor.
    // Returns { query, start, end } or null.
    // ------------------------------------------------------------------
    function getFragment() {
      const cursor = ta.selectionStart;
      const text = ta.value.slice(0, cursor);
      // Walk backwards: allow letters, digits, underscore, hyphen, plus.
      let i = cursor - 1;
      while (i >= 0 && /[\w\-+]/.test(text[i])) i--;
      // The character at i must be ':'.
      if (i < 0 || text[i] !== ':') return null;
      const query = text.slice(i + 1); // everything after the ':'
      if (query.length < 2) return null;
      return { query: query, start: i, end: cursor };
    }

    // ------------------------------------------------------------------
    // Input handler — search and render results
    // ------------------------------------------------------------------
    ta.addEventListener('input', () => {
      const frag = getFragment();
      if (!frag) {
        hideDropdown();
        return;
      }

      matchStart = frag.start;
      matchEnd = frag.end;

      db.getEmojiBySearchQuery(frag.query)
        .then((results) => {
          // Re-check cursor hasn't moved since the async call.
          const current = getFragment();
          if (!current || current.query !== frag.query) return;

          if (!results || results.length === 0) {
            hideDropdown();
            return;
          }

          const top = results.slice(0, 8);
          list.innerHTML = top
            .map((emoji, idx) => {
              const name = emoji.shortcodes?.[0] ? emoji.shortcodes[0] : emoji.annotation || '';
              return (
                '<li class="emoji-autocomplete__item"' +
                ' role="option"' +
                ' aria-selected="false"' +
                ' data-unicode="' +
                emoji.unicode +
                '"' +
                ' data-idx="' +
                idx +
                '">' +
                '<span class="emoji-autocomplete__glyph">' +
                emoji.unicode +
                '</span>' +
                '<span class="emoji-autocomplete__name">:' +
                name +
                ':</span>' +
                '</li>'
              );
            })
            .join('');

          positionDropdown();
          list.hidden = false;
          highlight(-1);

          list.querySelectorAll('.emoji-autocomplete__item').forEach((el) => {
            el.addEventListener('mousedown', (e) => {
              // mousedown fires before blur; prevent textarea losing focus.
              e.preventDefault();
              insertEmoji(el.dataset.unicode);
            });
            el.addEventListener('mouseover', () => {
              highlight(parseInt(el.dataset.idx, 10));
            });
          });
        })
        .catch(() => {
          hideDropdown();
        });
    });

    // ------------------------------------------------------------------
    // Keyboard navigation
    // ------------------------------------------------------------------
    ta.addEventListener('keydown', (e) => {
      if (list.hidden) return;
      const items = getItems();
      const count = items.length;
      if (count === 0) return;

      if (e.key === 'ArrowDown') {
        e.preventDefault();
        highlight((activeIdx + 1) % count);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        highlight((activeIdx - 1 + count) % count);
      } else if (e.key === 'Enter' || e.key === 'Tab') {
        const target = activeIdx >= 0 ? items[activeIdx] : items[0];
        if (target) {
          e.preventDefault();
          insertEmoji(target.dataset.unicode);
        }
      } else if (e.key === 'Escape') {
        e.preventDefault();
        hideDropdown();
      }
    });

    // Close on click outside.
    document.addEventListener('click', (e) => {
      if (!list.hidden && !list.contains(e.target) && e.target !== ta) {
        hideDropdown();
      }
    });

    // Close when textarea loses focus (defer so mousedown on item fires first).
    ta.addEventListener('blur', () => {
      setTimeout(() => {
        if (!list.hidden) hideDropdown();
      }, 150);
    });
  });
})();

// __myReactions tracks which emojis the current user has reacted with,
// keyed by message ID. Populated from the server-rendered DOM on load and
// kept up-to-date as reaction SSE events arrive.
// { [msgId]: Set<emoji> }
const __myReactions = (() => {
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
function applyMyReactions(barEl, msgId) {
  const mine = __myReactions[msgId];
  if (!mine || mine.size === 0) return;
  barEl.querySelectorAll('.reaction-pill').forEach((pill) => {
    if (mine.has(pill.dataset.emoji)) {
      pill.classList.add('reaction-pill--active');
    }
  });
}

// Second EventSource: handles unfurl, reaction, and version SSE events.
// HTMX manages its own EventSource for "message" events; custom event types
// use a dedicated connection so we can use native addEventListener.
(() => {
  let es = new EventSource(`/rooms/${roomID}/events`);

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
      const res = await fetch(`/rooms/${roomID}/messages?limit=50`);
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
        // Just set the HTML — the ResizeObserver will handle scrolling when
        // the unfurl image loads and the list grows.
        el.innerHTML = html;
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
      if (ev.reactorId === __currentUserID) {
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

  attachEsListeners(es);

  // Reload after a successful message send if a deploy was detected.
  const form = document.querySelector('.message-form');
  if (form) {
    form.addEventListener('htmx:afterRequest', () => {
      if (__pendingReload) window.location.reload();
    });
  }
  // -- End auto-reload ----------------------------------------------------

  // Close the EventSource when the page hides; reopen immediately when it
  // becomes visible again so reconnect happens without browser backoff delay.
  window.addEventListener('pagehide', () => { es.close(); });

  document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
      es.close();
    } else {
      es = new EventSource(`/rooms/${roomID}/events`);
      attachEsListeners(es);
      doCatchUp();
    }
  });

  window.addEventListener('pageshow', (e) => {
    if (e.persisted) { // restored from bfcache
      es = new EventSource(`/rooms/${roomID}/events`);
      attachEsListeners(es);
      doCatchUp();
    }
  });

  window.addEventListener('online', () => { doCatchUp(); });
})();

// ---- Optimistic delete UX ----
// On delete request: dim the message and replace the trash icon with a
// spinner so the user has clear feedback and can't double-submit.
// On failure: restore everything so the user can retry.
(() => {
  const SPINNER_HTML = '<span class="attachment-chip__spinner" aria-hidden="true"></span>';

  // Saved button contents keyed by the button element itself (WeakMap so
  // there's no need to manually clean up after the element is removed).
  const savedHTML = new WeakMap();

  function onBefore(e) {
    const btn = e.detail.elt;
    if (!btn || !btn.classList.contains('message__delete')) return;
    const article = btn.closest('article.message');
    if (!article) return;

    article.classList.add('message--deleting');
    savedHTML.set(btn, btn.innerHTML);
    btn.innerHTML = SPINNER_HTML;
    btn.disabled = true;
  }

  function onError(e) {
    const btn = e.detail.elt;
    if (!btn || !btn.classList.contains('message__delete')) return;
    const article = btn.closest('article.message');
    if (article) article.classList.remove('message--deleting');

    const html = savedHTML.get(btn);
    if (html !== undefined) btn.innerHTML = html;
    btn.disabled = false;
  }

  document.addEventListener('htmx:beforeRequest', onBefore);
  document.addEventListener('htmx:responseError', onError);
  document.addEventListener('htmx:sendError', onError);
})();

// ---- Inline message edit UX ----
// Uses event delegation so it works on messages inserted via SSE.
(() => {
  function openEdit(msgId) {
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

  function closeEdit(msgId) {
    const article = document.getElementById(`msg-${msgId}`);
    if (!article) return;
    const textEl = document.getElementById(`text-${msgId}`);
    const form = document.getElementById(`edit-form-${msgId}`);
    if (textEl) textEl.hidden = false;
    if (form) form.hidden = true;
  }

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

  // Expose openEdit / closeEdit for the keyboard navigation module.
  window.__openEdit = openEdit;
  window.__closeEdit = closeEdit;

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
})();

// ---- Keyboard-centric navigation ----
// Allows the user to navigate messages with arrow keys, edit with 'e',
// delete with 'd', and return to the textarea with Escape.
(() => {
  const composeTa = document.querySelector('.message-form__textarea');
  if (!composeTa) return;

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
        if (window.__closeEdit) window.__closeEdit(msgId);
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
      if (editTrigger && !editTrigger.hidden && window.__openEdit) {
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
})();

// ---- @mention autocomplete ----
// Mirrors the emoji shortcode autocomplete pattern but triggered by '@'.
(() => {
  const ta = document.querySelector('.message-form__textarea');
  const list = document.getElementById('mention-autocomplete');
  if (!ta || !list) return;

  let members = []; // cached [{id, name, avatar_url}]
  let activeIdx = -1;
  let matchStart = -1;
  let matchEnd = -1;

  function loadMembers() {
    fetch(`/rooms/${roomID}/members`, { credentials: 'same-origin' })
      .then((r) => r.json())
      .then((data) => {
        members = data || [];
      })
      .catch(() => {});
  }
  loadMembers();

  function getItems() {
    return list.querySelectorAll('.emoji-autocomplete__item');
  }

  function highlight(idx) {
    getItems().forEach((el, i) => {
      el.setAttribute('aria-selected', i === idx ? 'true' : 'false');
    });
    activeIdx = idx;
  }

  function hideDropdown() {
    list.hidden = true;
    list.innerHTML = '';
    activeIdx = -1;
    matchStart = -1;
    matchEnd = -1;
  }

  function positionDropdown() {
    const rect = ta.getBoundingClientRect();
    list.style.left = `${rect.left}px`;
    list.style.bottom = `${window.innerHeight - rect.top + 4}px`;
    list.style.width = `${rect.width}px`;
  }

  function insertMention(name) {
    if (matchStart < 0) return;
    const before = ta.value.slice(0, matchStart);
    const after = ta.value.slice(matchEnd);
    ta.value = `${before}@${name} ${after}`;
    const pos = matchStart + name.length + 2;
    ta.selectionStart = ta.selectionEnd = pos;
    ta.dispatchEvent(new Event('input'));
    ta.focus();
    hideDropdown();
  }

  // Returns { query, start, end } or null if cursor isn't after an @word.
  function getFragment() {
    const cursor = ta.selectionStart;
    const text = ta.value.slice(0, cursor);
    let i = cursor - 1;
    // Walk back through word chars + spaces (but not newlines)
    while (i >= 0 && /[^\n@]/.test(text[i])) i--;
    if (i < 0 || text[i] !== '@') return null;
    const query = text.slice(i + 1);
    if (query.length === 0) return null;
    return { query: query.toLowerCase(), start: i, end: cursor };
  }

  ta.addEventListener('input', () => {
    const frag = getFragment();
    if (!frag) {
      hideDropdown();
      return;
    }
    matchStart = frag.start;
    matchEnd = frag.end;

    const q = frag.query;
    const results = members.filter((m) => m.name.toLowerCase().startsWith(q)).slice(0, 8);

    if (results.length === 0) {
      hideDropdown();
      return;
    }

    list.innerHTML = results
      .map((m, idx) => {
        const initials = m.name.slice(0, 1).toUpperCase();
        const avatar = m.avatar_url
          ? '<img src="' +
            m.avatar_url +
            '" width="20" height="20" alt="" style="border-radius:50%;object-fit:cover;">'
          : '<span style="display:inline-flex;align-items:center;justify-content:center;width:20px;height:20px;border-radius:50%;background:var(--color-primary);color:#fff;font-size:.7rem;font-weight:600;">' +
            initials +
            '</span>';
        return (
          '<li class="emoji-autocomplete__item"' +
          ' role="option" aria-selected="false"' +
          ' data-name="' +
          m.name.replace(/"/g, '&quot;') +
          '"' +
          ' data-idx="' +
          idx +
          '">' +
          '<span class="emoji-autocomplete__glyph" style="width:auto;">' +
          avatar +
          '</span>' +
          '<span class="emoji-autocomplete__name">@' +
          m.name +
          '</span>' +
          '</li>'
        );
      })
      .join('');

    positionDropdown();
    list.hidden = false;
    highlight(-1);

    list.querySelectorAll('.emoji-autocomplete__item').forEach((el) => {
      el.addEventListener('mousedown', (e) => {
        e.preventDefault();
        insertMention(el.dataset.name);
      });
      el.addEventListener('mouseover', () => {
        highlight(parseInt(el.dataset.idx, 10));
      });
    });
  });

  ta.addEventListener('keydown', (e) => {
    if (list.hidden) return;
    const items = getItems();
    const count = items.length;
    if (count === 0) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      highlight((activeIdx + 1) % count);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      highlight((activeIdx - 1 + count) % count);
    } else if (e.key === 'Enter' || e.key === 'Tab') {
      const target = activeIdx >= 0 ? items[activeIdx] : items[0];
      if (target) {
        e.preventDefault();
        insertMention(target.dataset.name);
      }
    } else if (e.key === 'Escape') {
      e.preventDefault();
      hideDropdown();
    }
  });

  document.addEventListener('click', (e) => {
    if (!list.hidden && !list.contains(e.target) && e.target !== ta) hideDropdown();
  });
  ta.addEventListener('blur', () => {
    setTimeout(() => {
      if (!list.hidden) hideDropdown();
    }, 150);
  });

  // Refresh member list when a new message is received (someone new may have joined).
  document.body.addEventListener('htmx:sseMessage', loadMembers);
})();

// ---- Notifications: SW registration, push subscription, mute, leader election, chime ----
(() => {
  // ------------------------------------------------------------------
  // BroadcastChannel leader election
  // The tab with the most recent heartbeat timestamp is the "leader".
  // Only the leader plays the in-tab chime / shows an in-page toast.
  // ------------------------------------------------------------------
  const TAB_ID = Math.random().toString(36).slice(2);
  const lastHeartbeats = {}; // tabId → timestamp
  lastHeartbeats[TAB_ID] = Date.now();

  let bc;
  try {
    bc = new BroadcastChannel('msg-notifications');
  } catch (_) {
    bc = null;
  }

  function broadcastHeartbeat() {
    lastHeartbeats[TAB_ID] = Date.now();
    if (bc) bc.postMessage({ type: 'heartbeat', tabId: TAB_ID, ts: Date.now() });
  }

  function isLeader() {
    // This tab is the leader if it has the highest (most recent) heartbeat.
    const myTs = lastHeartbeats[TAB_ID] || 0;
    for (const id in lastHeartbeats) {
      if (id !== TAB_ID && lastHeartbeats[id] > myTs) return false;
    }
    return true;
  }

  if (bc) {
    bc.onmessage = (e) => {
      if (e.data && e.data.type === 'heartbeat') {
        lastHeartbeats[e.data.tabId] = e.data.ts;
      }
    };
  }

  // Broadcast on visibility change and focus events so leadership transfers
  // to the newly active tab quickly.
  document.addEventListener('visibilitychange', broadcastHeartbeat);
  window.addEventListener('focus', broadcastHeartbeat);
  // Initial heartbeat + periodic keepalive (every 5s).
  broadcastHeartbeat();
  setInterval(broadcastHeartbeat, 5000);

  // ------------------------------------------------------------------
  // In-tab chime + toast
  // ------------------------------------------------------------------
  let chimeAudio = null;

  function playChime() {
    try {
      if (!chimeAudio) chimeAudio = new Audio(CHIME_URL);
      // Clone so rapid messages don't block.
      chimeAudio
        .cloneNode()
        .play()
        .catch(() => {});
    } catch (_) {}
  }

  function shouldNotifyInTab() {
    // Only notify if this tab is the leader AND the document is hidden
    // (i.e. user isn't actively looking at this tab).
    return isLeader() && document.hidden;
  }

  // Listen to HTMX SSE message events (fires when a new message is injected).
  document.body.addEventListener('htmx:sseMessage', () => {
    if (shouldNotifyInTab()) {
      playChime();
    }
  });

  // ------------------------------------------------------------------
  // Service Worker registration
  // ------------------------------------------------------------------
  let swRegistration = null;
  const isIOS =
    /iPhone|iPad|iPod/.test(navigator.userAgent) ||
    (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  const needsPWAGuide = isIOS && navigator.standalone !== true;

  const bell = document.getElementById('notif-bell');
  const popover = document.getElementById('notif-popover');
  const unmuteBtn = document.getElementById('notif-unmute');
  const unsubBtn = document.getElementById('notif-unsubscribe');
  const pwaGuide = document.getElementById('ios-pwa-guide');

  if (needsPWAGuide) {
    // iOS Safari (browser mode): push not supported, don't wait for SW/push checks.
    setBellState('off');
  } else if ('serviceWorker' in navigator) {
    // Secondary update trigger: fires when a new SW takes over via
    // skipWaiting + clients.claim. Guard against first-install noise by
    // only reacting when a SW was already controlling the page before.
    const prevController = navigator.serviceWorker.controller;
    navigator.serviceWorker.addEventListener('controllerchange', () => {
      if (!prevController) return; // first install — not a real update
      if (!document.hasFocus()) {
        window.location.reload();
        return;
      }
      const hint = document.getElementById('update-hint');
      if (hint) hint.hidden = false;
    });

    navigator.serviceWorker
      .register('/sw.js', { scope: '/' })
      .then((reg) => {
        swRegistration = reg;
        // Sync push subscription state with our stored UI state.
        reg.pushManager.getSubscription().then((sub) => {
          if (sub) {
            setBellState('on');
            checkMuteState();
          } else {
            setBellState('off');
          }
        });
      })
      .catch((err) => {
        console.warn('SW registration failed:', err);
      });
  } else {
    setBellState('off');
  }

  // ------------------------------------------------------------------
  // Bell UI state
  // ------------------------------------------------------------------
  if (pwaGuide) {
    function closeGuide() {
      pwaGuide.classList.add('is-closing');
      pwaGuide.addEventListener('animationend', function handler() {
        pwaGuide.removeEventListener('animationend', handler);
        pwaGuide.classList.remove('is-closing');
        pwaGuide.close();
      });
    }

    pwaGuide.querySelector('.pwa-guide__close').addEventListener('click', closeGuide);
    pwaGuide.addEventListener('click', (e) => {
      if (e.target === pwaGuide) closeGuide();
    });
  }

  function setBellState(state, muteUntil) {
    if (!bell) return;
    bell.dataset.state = state;
    bell.disabled = state === 'loading' || state === 'pending';
    const labels = {
      off: 'Enable notifications',
      on: 'Notification settings',
      muted: 'Notifications muted — click to manage',
      loading: 'Enabling notifications…',
    };
    bell.setAttribute('aria-label', labels[state] || 'Notifications');
    // Store mute expiry so the hover tooltip can show remaining time.
    if (state === 'muted' && muteUntil) {
      bell.dataset.muteUntil = muteUntil; // ISO8601 or "forever"
    } else {
      delete bell.dataset.muteUntil;
      bell.removeAttribute('title');
    }
  }

  // Format remaining mute time into a human-readable string.
  function formatMuteRemaining(muteUntil) {
    if (!muteUntil || muteUntil === 'forever') return 'Muted indefinitely';
    const ms = new Date(muteUntil).getTime() - Date.now();
    if (ms <= 0) return null; // expired
    const mins = Math.round(ms / 60000);
    const hours = Math.round(ms / 3600000);
    const days = Math.round(ms / 86400000);
    if (days >= 2) return `Muted for ${days} more days`;
    if (hours >= 2) return `Muted for ${hours} more hours`;
    if (mins >= 2) return `Muted for ${mins} more minutes`;
    return 'Muted for less than a minute';
  }

  // Update the native title tooltip on hover when muted.
  if (bell) {
    bell.addEventListener('mouseenter', () => {
      if (bell.dataset.state !== 'muted') return;
      const text = formatMuteRemaining(bell.dataset.muteUntil);
      if (text) bell.setAttribute('title', text);
      else bell.removeAttribute('title');
    });
  }

  function checkMuteState() {
    fetch('/settings/mute', { credentials: 'same-origin' })
      .then((r) => r.json())
      .then((data) => {
        if (data.muted) {
          setBellState('muted', data.until);
          if (unmuteBtn) unmuteBtn.hidden = false;
        } else {
          setBellState('on');
          if (unmuteBtn) unmuteBtn.hidden = true;
        }
      })
      .catch(() => {});
  }

  // ------------------------------------------------------------------
  // Push subscription helpers
  // ------------------------------------------------------------------
  function urlBase64ToUint8Array(base64String) {
    const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
    const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
    const rawData = atob(base64);
    const output = new Uint8Array(rawData.length);
    for (let i = 0; i < rawData.length; i++) output[i] = rawData.charCodeAt(i);
    return output;
  }

  function subscribeForPush() {
    if (!swRegistration) return Promise.reject(new Error('SW not ready'));
    return fetch('/push/vapid-public-key', { credentials: 'same-origin' })
      .then((r) => r.json())
      .then((data) =>
        swRegistration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: urlBase64ToUint8Array(data.key),
        }),
      )
      .then((sub) =>
        fetch('/push/subscribe', {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(sub.toJSON()),
        }).then(() => sub),
      );
  }

  function unsubscribeFromPush() {
    if (!swRegistration) return Promise.resolve();
    return swRegistration.pushManager.getSubscription().then((sub) => {
      if (!sub) return;
      const endpoint = sub.endpoint;
      return sub.unsubscribe().then(() =>
        fetch('/push/subscribe', {
          method: 'DELETE',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ endpoint: endpoint }),
        }),
      );
    });
  }

  // ------------------------------------------------------------------
  // Bell click handler
  // ------------------------------------------------------------------
  if (bell) {
    bell.addEventListener('click', (e) => {
      e.stopPropagation();
      const state = bell.dataset.state;

      if (state === 'off') {
        if (needsPWAGuide) {
          pwaGuide?.showModal();
          return;
        }
        setBellState('loading');
        Notification.requestPermission().then((perm) => {
          if (perm !== 'granted') {
            setBellState('off');
            return;
          }
          subscribeForPush()
            .then(() => {
              setBellState('on');
            })
            .catch((err) => {
              console.warn('Push subscribe failed:', err);
              setBellState('off');
            });
        });
        return;
      }

      // Subscribed or muted — toggle popover.
      if (popover) {
        const isOpen = !popover.hidden;
        popover.hidden = isOpen;
      }
    });
  }

  // Mute duration buttons.
  const MUTE_MS = { '1h': 3600000, '8h': 28800000, '24h': 86400000, '168h': 604800000 };
  if (popover) {
    popover.querySelectorAll('[data-mute]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const dur = btn.dataset.mute;
        // Compute until timestamp client-side so the tooltip works immediately
        // without a round-trip. "forever" stays as the sentinel string.
        const until =
          dur === 'forever' ? 'forever' : new Date(Date.now() + MUTE_MS[dur]).toISOString();
        fetch('/settings/mute', {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ duration: dur }),
        }).then(() => {
          setBellState('muted', until);
          if (unmuteBtn) unmuteBtn.hidden = false;
          if (popover) popover.hidden = true;
        });
      });
    });
  }

  // Unmute button.
  if (unmuteBtn) {
    unmuteBtn.addEventListener('click', () => {
      fetch('/settings/mute', { method: 'DELETE', credentials: 'same-origin' }).then(() => {
        setBellState('on');
        unmuteBtn.hidden = true;
        if (popover) popover.hidden = true;
      });
    });
  }

  // Turn off notifications (unsubscribe).
  if (unsubBtn) {
    unsubBtn.addEventListener('click', () => {
      unsubscribeFromPush().then(() => {
        setBellState('off');
        if (popover) popover.hidden = true;
      });
    });
  }

  // Close popover on outside click.
  document.addEventListener('click', (e) => {
    if (popover && !popover.hidden) {
      const wrap = document.getElementById('notif-wrap');
      if (wrap && !wrap.contains(e.target)) popover.hidden = true;
    }
  });
})();

// ---- Profile popover ----
(() => {
  const profileBtn = document.getElementById('profile-btn');
  const profilePopover = document.getElementById('profile-popover');
  if (!profileBtn || !profilePopover) return;

  profileBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    profilePopover.hidden = !profilePopover.hidden;
  });

  document.addEventListener('click', (e) => {
    if (!profilePopover.hidden) {
      const wrap = document.getElementById('profile-wrap');
      if (wrap && !wrap.contains(e.target)) profilePopover.hidden = true;
    }
  });
})();

// ---- Reaction picker ----
// Clicking the "add reaction" button opens the global emoji picker in reaction
// mode, positioned above the button. Selecting an emoji POSTs the reaction.
(() => {
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
  // Capture on document fires before any bubble listener (including app.js) and
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
      fetch(`/rooms/${roomID}/messages/${msgId}/reactions`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body.toString(),
      });
    },
    { capture: true },
  );
})();

// ---- Unread message count (title prefix + favicon badge) ----
(() => {
  let unreadCount = 0;
  const originalTitle = document.title;
  const faviconEl = document.querySelector('link[rel="icon"]');
  const FAVICON_NORMAL = faviconEl ? faviconEl.href : null;
  const FAVICON_BADGE = '/static/favicon-badge.svg';

  function setUnread(n) {
    unreadCount = n;
    document.title = n > 0 ? `[${n}] ${originalTitle}` : originalTitle;
    if (faviconEl) faviconEl.href = n > 0 ? FAVICON_BADGE : FAVICON_NORMAL;
  }

  // Increment when a message arrives while the tab is hidden and it's not from the current user.
  document.body.addEventListener('htmx:sseMessage', () => {
    if (!document.hidden) return;
    const target = document.getElementById('sse-message-target');
    const msg = target?.previousElementSibling;
    if (msg && msg.dataset.authorId !== __currentUserID) {
      setUnread(unreadCount + 1);
    }
  });

  // Reset when the tab becomes visible.
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) setUnread(0);
  });
})();
