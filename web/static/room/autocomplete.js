// Emoji shortcode + @mention autocomplete for the message textarea and edit
// textareas. Both dropdowns share the same positioning and keyboard-navigation
// pattern. All event listeners are registered synchronously at module load so
// that listener order is deterministic (autocomplete before edit.js).

// ---- Shared helpers ----

function isAutocompleteTextarea(el) {
  return (
    el instanceof HTMLTextAreaElement &&
    (el.classList.contains('message-form__textarea') ||
      el.classList.contains('message-edit-form__textarea'))
  );
}

function createDropdown(listEl) {
  let activeIdx = -1;
  let matchStart = -1;
  let matchEnd = -1;

  function getItems() {
    return listEl.querySelectorAll('.emoji-autocomplete__item');
  }

  function highlight(idx) {
    getItems().forEach((el, i) => {
      el.setAttribute('aria-selected', i === idx ? 'true' : 'false');
    });
    activeIdx = idx;
  }

  function hide() {
    listEl.hidden = true;
    listEl.innerHTML = '';
    activeIdx = -1;
    matchStart = -1;
    matchEnd = -1;
  }

  // position is called with the currently active textarea.
  function position(taEl) {
    const rect = taEl.getBoundingClientRect();
    listEl.style.left = `${rect.left}px`;
    listEl.style.bottom = `${window.innerHeight - rect.top + 4}px`;
    listEl.style.width = `${rect.width}px`;
  }

  return {
    get activeIdx() { return activeIdx; },
    get matchStart() { return matchStart; },
    set matchStart(v) { matchStart = v; },
    get matchEnd() { return matchEnd; },
    set matchEnd(v) { matchEnd = v; },
    getItems,
    highlight,
    hide,
    position,
  };
}

// ---- Emoji shortcode autocomplete ----
// Listeners are registered synchronously; the DB is loaded asynchronously.
// The input handler bails early if the DB is not yet ready.
(function initEmojiAutocomplete() {
  const listEl = document.getElementById('emoji-autocomplete');
  if (!listEl) return;

  const dd = createDropdown(listEl);
  let activeTextarea = null;
  let db = null;
  let allEmojis = [];

  (function waitForDb() {
    function init(database) {
      db = database;
      Promise.all([0, 1, 2, 3, 4, 5, 6, 7, 8, 9].map((g) => db.getEmojiByGroup(g)))
        .then((groups) => { allEmojis = groups.flat(); })
        .catch(() => {});
    }
    if (window.__EmojiDatabase) {
      init(new window.__EmojiDatabase());
      return;
    }
    const t = setInterval(() => {
      if (window.__EmojiDatabase) {
        clearInterval(t);
        init(new window.__EmojiDatabase());
      }
    }, 50);
  })();

  function fuzzyScore(pattern, str) {
    let pi = 0, score = 0, lastMatch = -1;
    for (let si = 0; si < str.length && pi < pattern.length; si++) {
      if (str[si] === pattern[pi]) {
        score += lastMatch === si - 1 ? 0 : si + 1;
        lastMatch = si;
        pi++;
      }
    }
    return pi === pattern.length ? score : Infinity;
  }

  function fuzzySearch(query) {
    const q = query.toLowerCase();
    const scored = [];
    for (const emoji of allEmojis) {
      const candidates = [...(emoji.shortcodes ?? []), emoji.annotation ?? ''].filter(Boolean);
      let best = Infinity;
      for (const c of candidates) {
        const s = fuzzyScore(q, c.toLowerCase());
        if (s < best) best = s;
      }
      if (best !== Infinity) scored.push({ emoji, score: best });
    }
    scored.sort((a, b) => a.score - b.score);
    return scored.map((x) => x.emoji);
  }

  function getFragment(ta) {
    const cursor = ta.selectionStart;
    const text = ta.value.slice(0, cursor);
    let i = cursor - 1;
    while (i >= 0 && /[\w\-+]/.test(text[i])) i--;
    if (i < 0 || text[i] !== ':') return null;
    const query = text.slice(i + 1);
    if (query.length < 2) return null;
    return { query, start: i, end: cursor };
  }

  function insertEmoji(unicode) {
    if (!activeTextarea || dd.matchStart < 0) return;
    const ta = activeTextarea;
    const before = ta.value.slice(0, dd.matchStart);
    const after = ta.value.slice(dd.matchEnd);
    ta.value = before + unicode + after;
    const pos = dd.matchStart + unicode.length;
    ta.selectionStart = ta.selectionEnd = pos;
    ta.dispatchEvent(new Event('input'));
    ta.focus();
    dd.hide();
  }

  document.addEventListener('input', (e) => {
    const ta = e.target;
    if (!isAutocompleteTextarea(ta)) return;
    if (!db) return;
    activeTextarea = ta;

    const frag = getFragment(ta);
    if (!frag) { dd.hide(); return; }

    dd.matchStart = frag.start;
    dd.matchEnd = frag.end;

    Promise.all([
      db.getEmojiBySearchQuery(frag.query).catch(() => []),
      Promise.resolve(fuzzySearch(frag.query)),
    ]).then(([wordResults, fuzzyResults]) => {
        const seen = new Set(wordResults.map((e) => e.unicode));
        const results = [...wordResults, ...fuzzyResults.filter((e) => !seen.has(e.unicode))];

        const current = getFragment(ta);
        if (!current || current.query !== frag.query) return;

        if (!results || results.length === 0) { dd.hide(); return; }

        const top = results.slice(0, 8);
        listEl.innerHTML = top
          .map((emoji, idx) => {
            const name = emoji.shortcodes?.[0] ? emoji.shortcodes[0] : emoji.annotation || '';
            return (
              '<li class="emoji-autocomplete__item"' +
              ' role="option" aria-selected="false"' +
              ' data-unicode="' + emoji.unicode + '"' +
              ' data-idx="' + idx + '">' +
              '<span class="emoji-autocomplete__glyph">' + emoji.unicode + '</span>' +
              '<span class="emoji-autocomplete__name">:' + name + ':</span>' +
              '</li>'
            );
          })
          .join('');

        dd.position(ta);
        listEl.hidden = false;
        dd.highlight(-1);

        listEl.querySelectorAll('.emoji-autocomplete__item').forEach((el) => {
          el.addEventListener('mousedown', (e) => {
            e.preventDefault();
            insertEmoji(el.dataset.unicode);
          });
          el.addEventListener('mouseover', () => {
            dd.highlight(parseInt(el.dataset.idx, 10));
          });
        });
      })
      .catch(() => { dd.hide(); });
  });

  document.addEventListener('keydown', (e) => {
    if (listEl.hidden) return;
    if (!isAutocompleteTextarea(e.target)) return;
    const items = dd.getItems();
    const count = items.length;
    if (count === 0) return;

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      dd.highlight((dd.activeIdx + 1) % count);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      dd.highlight((dd.activeIdx - 1 + count) % count);
    } else if (e.key === 'Enter' || e.key === 'Tab') {
      const target = dd.activeIdx >= 0 ? items[dd.activeIdx] : items[0];
      if (target) { e.preventDefault(); insertEmoji(target.dataset.unicode); }
    } else if (e.key === 'Escape') {
      e.preventDefault();
      dd.hide();
    }
  });

  document.addEventListener('click', (e) => {
    if (!listEl.hidden && !listEl.contains(e.target) && !isAutocompleteTextarea(e.target)) dd.hide();
  });

  // focusout bubbles, unlike blur — use it for event delegation.
  // Skip if focus is moving to another autocomplete textarea (e.g. main→edit),
  // to avoid hiding a dropdown that the new textarea is about to open.
  document.addEventListener('focusout', (e) => {
    if (!isAutocompleteTextarea(e.target)) return;
    if (isAutocompleteTextarea(e.relatedTarget)) return;
    setTimeout(() => { if (!listEl.hidden) dd.hide(); }, 150);
  });
})();

// ---- @mention autocomplete ----
(function initMentionAutocomplete() {
  const listEl = document.getElementById('mention-autocomplete');
  if (!listEl) return;

  const dd = createDropdown(listEl);
  let activeTextarea = null;
  let members = []; // cached [{id, name, avatar_url}]

  function loadMembers() {
    fetch(`/rooms/${window.roomID}/members`, { credentials: 'same-origin' })
      .then((r) => r.json())
      .then((data) => { members = data || []; })
      .catch(() => {});
  }
  loadMembers();

  function getFragment(ta) {
    const cursor = ta.selectionStart;
    const text = ta.value.slice(0, cursor);
    let i = cursor - 1;
    while (i >= 0 && /[^\n@]/.test(text[i])) i--;
    if (i < 0 || text[i] !== '@') return null;
    const query = text.slice(i + 1);
    if (query.length === 0) return null;
    return { query: query.toLowerCase(), start: i, end: cursor };
  }

  function insertMention(name) {
    if (!activeTextarea || dd.matchStart < 0) return;
    const ta = activeTextarea;
    const before = ta.value.slice(0, dd.matchStart);
    const after = ta.value.slice(dd.matchEnd);
    ta.value = `${before}@${name} ${after}`;
    const pos = dd.matchStart + name.length + 2;
    ta.selectionStart = ta.selectionEnd = pos;
    ta.dispatchEvent(new Event('input'));
    ta.focus();
    dd.hide();
  }

  document.addEventListener('input', (e) => {
    const ta = e.target;
    if (!isAutocompleteTextarea(ta)) return;
    activeTextarea = ta;

    const frag = getFragment(ta);
    if (!frag) { dd.hide(); return; }

    dd.matchStart = frag.start;
    dd.matchEnd = frag.end;

    const q = frag.query;
    const results = members.filter((m) => m.name.toLowerCase().startsWith(q)).slice(0, 8);

    if (results.length === 0) { dd.hide(); return; }

    listEl.innerHTML = results
      .map((m, idx) => {
        const initials = m.name.slice(0, 1).toUpperCase();
        const avatar = m.avatar_url
          ? `<img src="${m.avatar_url}" width="20" height="20" alt="" style="border-radius:50%;object-fit:cover;">`
          : `<span style="display:inline-flex;align-items:center;justify-content:center;width:20px;height:20px;border-radius:50%;background:var(--color-primary);color:#fff;font-size:.7rem;font-weight:600;">${initials}</span>`;
        return (
          '<li class="emoji-autocomplete__item"' +
          ' role="option" aria-selected="false"' +
          ' data-name="' + m.name.replace(/"/g, '&quot;') + '"' +
          ' data-idx="' + idx + '">' +
          '<span class="emoji-autocomplete__glyph" style="width:auto;">' + avatar + '</span>' +
          '<span class="emoji-autocomplete__name">@' + m.name + '</span>' +
          '</li>'
        );
      })
      .join('');

    dd.position(ta);
    listEl.hidden = false;
    dd.highlight(-1);

    listEl.querySelectorAll('.emoji-autocomplete__item').forEach((el) => {
      el.addEventListener('mousedown', (e) => {
        e.preventDefault();
        insertMention(el.dataset.name);
      });
      el.addEventListener('mouseover', () => {
        dd.highlight(parseInt(el.dataset.idx, 10));
      });
    });
  });

  document.addEventListener('keydown', (e) => {
    if (listEl.hidden) return;
    if (!isAutocompleteTextarea(e.target)) return;
    const items = dd.getItems();
    const count = items.length;
    if (count === 0) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      dd.highlight((dd.activeIdx + 1) % count);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      dd.highlight((dd.activeIdx - 1 + count) % count);
    } else if (e.key === 'Enter' || e.key === 'Tab') {
      const target = dd.activeIdx >= 0 ? items[dd.activeIdx] : items[0];
      if (target) { e.preventDefault(); insertMention(target.dataset.name); }
    } else if (e.key === 'Escape') {
      e.preventDefault();
      dd.hide();
    }
  });

  document.addEventListener('click', (e) => {
    if (!listEl.hidden && !listEl.contains(e.target) && !isAutocompleteTextarea(e.target)) dd.hide();
  });

  document.addEventListener('focusout', (e) => {
    if (!isAutocompleteTextarea(e.target)) return;
    if (isAutocompleteTextarea(e.relatedTarget)) return;
    setTimeout(() => { if (!listEl.hidden) dd.hide(); }, 150);
  });

  // Refresh member list when a new message is received (someone new may have joined).
  document.body.addEventListener('htmx:sseMessage', loadMembers);
})();
