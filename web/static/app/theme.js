// Theme toggle: persist choice in localStorage, fall back to OS preference.
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
