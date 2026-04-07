/**
 * Volon mini-site theme toggle
 * Light/dark mode via localStorage + class strategy on <html>
 */

function toggleTheme() {
  const isDark = document.documentElement.classList.toggle('dark');
  localStorage.setItem('volon-theme', isDark ? 'dark' : 'light');
}

// Apply saved theme on load (also runs inline in <head> to prevent flash,
// but this ensures consistency after DOMContentLoaded as well)
(function () {
  var s = localStorage.getItem('volon-theme');
  var d = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
  if (s === 'dark' || (!s && d)) {
    document.documentElement.classList.add('dark');
  } else {
    document.documentElement.classList.remove('dark');
  }
})();
