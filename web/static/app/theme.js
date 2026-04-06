// Theme: persist choice in localStorage, fall back to OS preference.
const root = document.documentElement;
const stored = localStorage.getItem('theme');
if (stored) root.setAttribute('data-theme', stored);

function syncThemeSwitcher() {
  const current = root.getAttribute('data-theme') || 'auto';
  document.querySelectorAll('[data-theme-value]').forEach((btn) => {
    btn.setAttribute('aria-checked', btn.dataset.themeValue === current ? 'true' : 'false');
  });
}

document.addEventListener('click', (e) => {
  const btn = e.target.closest('[data-theme-value]');
  if (!btn) return;
  const value = btn.dataset.themeValue;
  root.setAttribute('data-theme', value);
  localStorage.setItem('theme', value);
  syncThemeSwitcher();
});

syncThemeSwitcher();
window.__syncThemeSwitcher = syncThemeSwitcher;
