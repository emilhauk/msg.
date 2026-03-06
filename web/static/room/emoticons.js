// ASCII emoticon → emoji replacement.
// Map of ASCII emoticon → unicode emoji. Order matters for the regex:
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

function escapeRe(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
const pattern = new RegExp(
  `(?:^|(?<=\\s))(${Object.keys(EMOTICONS).map(escapeRe).join('|')})(?=\\s|$)`,
  'g',
);

// Replace all emoticons in a plain string (used for the pre-submit full pass).
export function replaceAllEmoticons(text) {
  return text.replace(pattern, (m) => EMOTICONS[m] || m);
}

// Replace the emoticon immediately to the left of the cursor, if the
// character just typed is a word boundary (space or newline).
// Returns true if a replacement was made so callers can skip other logic.
export function replaceAtCursor(ta) {
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
