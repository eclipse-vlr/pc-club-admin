import { useEffect } from 'react';

// Presentation only: canceled transitions never control navigation or form state.
export function useInterfaceMotion(section: string, loginMode: string, actor: string, ready: boolean) {
  useEffect(() => {
    if (!ready) return;
    const preference = window.matchMedia('(prefers-reduced-motion: reduce)');
    let animation: Animation | undefined;
    const cancel = () => animation?.cancel();
    if (!preference.matches && document.documentElement.dataset.motion !== 'off') {
      const target = actor ? document.getElementById(section.slice(1)) : document.querySelector<HTMLElement>('.login-card > form');
      if (target && typeof target.animate === 'function') {
        animation = target.animate([
          { opacity: 0.55, transform: 'translateY(14px)' },
          { opacity: 1, transform: 'translateY(0)' },
        ], { duration: 420, easing: 'cubic-bezier(.2,.8,.2,1)' });
      }
    }
    preference.addEventListener('change', cancel);
    window.addEventListener('pcclub:motion', cancel);
    return () => {
      cancel(); preference.removeEventListener('change', cancel);
      window.removeEventListener('pcclub:motion', cancel);
    };
  }, [section, loginMode, actor, ready]);
}
