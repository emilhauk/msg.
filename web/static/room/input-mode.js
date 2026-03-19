// Tracks whether the user is likely using a virtual (touch) or physical keyboard.
//
// Strategy: default to `pointer: coarse` media query (covers phones/tablets),
// then let actual pointer events override — so a tablet user who attaches a
// Bluetooth keyboard (and starts using a mouse/trackpad) gets physical-keyboard
// behavior, and vice versa.
//
// Exported flag `virtualKeyboard` is read by the Enter-to-send logic in
// room.html (inline handler) and edit.js to decide whether Enter inserts a
// newline (virtual) or submits the form (physical).

const coarse = matchMedia('(pointer: coarse)');
let _virtual = coarse.matches;

// Live media-query changes (e.g. tablet docked/undocked).
coarse.addEventListener('change', (e) => { _virtual = e.matches; });

// Override based on actual pointer interaction with any textarea.
document.addEventListener('pointerdown', (e) => {
  if (!e.target || !e.target.closest('textarea')) return;
  _virtual = e.pointerType === 'touch';
});

/** @returns {boolean} true when the user is likely using a virtual keyboard */
export function isVirtualKeyboard() {
  return _virtual;
}

// Expose globally so the inline onkeydown in room.html can access it.
window.__isVirtualKeyboard = isVirtualKeyboard;
