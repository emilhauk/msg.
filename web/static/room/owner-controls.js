// applyOwnerControls unhides the edit and delete buttons on a message article
// when the current user is the author. Called at page load and after every
// SSE message insert or edit replacement so buttons are never baked into the
// shared SSE broadcast HTML.
export function applyOwnerControls(articleEl) {
  if (!articleEl || articleEl.dataset.authorId !== window.__currentUserID) return;
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
