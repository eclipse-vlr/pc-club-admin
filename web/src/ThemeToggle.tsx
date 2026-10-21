import { useState } from 'react';

type Theme = 'dark' | 'light';
const preferenceKey = 'pcclub.theme';

// Only a visual preference is stored. Authentication remains in HttpOnly cookies.
function initialTheme(): Theme {
  try { return localStorage.getItem(preferenceKey) === 'light' ? 'light' : 'dark'; }
  catch { return 'dark'; }
}

const initial = initialTheme();
document.documentElement.dataset.theme = initial;

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(initial);
  function toggle() {
    const next = theme === 'dark' ? 'light' : 'dark';
    document.documentElement.dataset.theme = next;
    setTheme(next);
    try { localStorage.setItem(preferenceKey, next); }
    catch { /* The theme still works when browser storage is unavailable. */ }
  }
  return <button className="theme-toggle" type="button" onClick={toggle} aria-label="Тёмная тема" aria-pressed={theme === 'dark'}>
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true"><path d="M20.9 13.1A9 9 0 0 1 10.9 3.1 9 9 0 1 0 20.9 13.1Z" /></svg>
    <span>{theme === 'dark' ? 'Тёмная тема' : 'Светлая тема'}</span>
  </button>;
}
