import { applyOwnerControls } from '/static/room/owner-controls.js';

// snapUnlessScrolledUp is exported so other modules (e.g. sse.js) can call it.
// It is re-assigned below once the message list element is found.
// ES module live bindings ensure importers always see the current value.
export let snapUnlessScrolledUp = () => {};

// Attach onload listeners to any not-yet-loaded images inside a container so
// that snapUnlessScrolledUp() is called once each image settles.
export function attachImageLoadSnap(container) {
  container.querySelectorAll('img').forEach((img) => {
    if (!img.complete) {
      img.addEventListener('load', () => snapUnlessScrolledUp(), { once: true });
    }
  });
}

const list = document.getElementById('message-list');
const content = document.getElementById('message-list-content');
if (list) {
  let userScrolledUp = false;

  // Snap to bottom instantly (no smooth animation so repeated ResizeObserver
  // callbacks can't race with an in-progress smooth scroll).
  function snapToBottom() {
    list.scrollTop = list.scrollHeight;
  }

  snapUnlessScrolledUp = () => {
    if (!userScrolledUp) snapToBottom();
  };

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
  // videos, or unfurl cards load and expand the layout.
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
      attachImageLoadSnap(target.previousElementSibling);
    }
  });
}
