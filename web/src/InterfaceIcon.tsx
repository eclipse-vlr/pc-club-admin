type IconName = 'shift' | 'history' | 'overview' | 'reports' | 'team' | 'arrow' | 'shield';

const paths: Record<IconName, string> = {
  shift: 'M8 5H5v16h14V5h-3M8 3h8v4H8zM8 12l2 2 5-5M8 18h7',
  history: 'M3 11a9 9 0 1 1 2 7M3 4v7h7M12 7v5l3 2',
  overview: 'M3 3h7v7H3zM14 3h7v7h-7zM3 14h7v7H3zM14 14h7v7h-7z',
  reports: 'M4 3v18h17M8 16v-5M13 16V7M18 16v-8',
  team: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M17 4a4 4 0 0 1 0 7M22 21v-2a4 4 0 0 0-3-3.9',
  arrow: 'M4 12h16M14 6l6 6-6 6',
  shield: 'M12 3l8 3v6c0 5-8 9-8 9s-8-4-8-9V6zM8 12l3 3 5-6',
};

export function InterfaceIcon({ name }: { name: IconName }) {
  return <svg className="interface-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={paths[name]} /></svg>;
}
