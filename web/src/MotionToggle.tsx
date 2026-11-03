import { useEffect, useState } from 'react';

const preferenceKey = 'pcclub.motion';
const systemPreference = window.matchMedia('(prefers-reduced-motion: reduce)');
function initialPreference() {
  try { return localStorage.getItem(preferenceKey) !== 'off'; }
  catch { return true; }
}
document.documentElement.dataset.motion = initialPreference() && !systemPreference.matches ? 'on' : 'off';

export function MotionToggle() {
  const [enabled, setEnabled] = useState(initialPreference);
  const [systemReduced, setSystemReduced] = useState(systemPreference.matches);
  useEffect(() => {
    const update = () => setSystemReduced(systemPreference.matches);
    systemPreference.addEventListener('change', update);
    return () => systemPreference.removeEventListener('change', update);
  }, []);
  const active = enabled && !systemReduced;
  useEffect(() => {
    document.documentElement.dataset.motion = active ? 'on' : 'off';
    window.dispatchEvent(new Event('pcclub:motion'));
  }, [active]);
  return <button className="motion-toggle" type="button" aria-label="Анимации интерфейса" aria-pressed={active} disabled={systemReduced} title={systemReduced ? 'Анимации отключены системной настройкой' : 'Включить или отключить анимации'} onClick={() => {
    const next = !enabled; setEnabled(next);
    try { localStorage.setItem(preferenceKey, next ? 'on' : 'off'); }
    catch { /* The preference also works without persistent storage. */ }
  }}><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" aria-hidden="true"><path d="M3 7h12a3 3 0 1 0-3-3M3 12h16a3 3 0 1 1-3 3M3 17h6a2 2 0 1 1-2 2" /></svg><span>Анимации</span></button>;
}
