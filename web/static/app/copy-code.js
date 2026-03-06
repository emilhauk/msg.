// Click-to-copy for code blocks.
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
