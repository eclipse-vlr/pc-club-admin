import { blockerHint } from './action-feedback';

export type Blocker = { code: string; item_code?: string; message: string };

export function blockerTarget(blocker: Blocker): string | null {
  if (blocker.code === 'NOTE_REQUIRED' || blocker.code === 'UNSAVED_NOTE') return 'shift-notes';
  if (blocker.item_code) return 'checklist-' + blocker.item_code;
  if (blocker.code === 'CHECKLIST_INCOMPLETE') return 'shift-acceptance';
  if (blocker.code.startsWith('CASH_') || blocker.code === 'OPENING_COUNT_REQUIRED') return 'shift-cash';
  if (blocker.code === 'RECEIPT_REQUIRED' || blocker.code === 'RECEIPT_MISMATCH') return 'handover-receipt';
  if (blocker.code === 'NOTE_ACK_REQUIRED') return 'handover-notes';
  if (blocker.code === 'READINESS_UNKNOWN' || blocker.code === 'STORAGE_UNKNOWN') return 'shift-reload';
  if (blocker.code === 'ACTION_UNKNOWN') return 'handover-reconcile';
  return null;
}

export function ActionRequirements({ id, blockers, onNavigate }: { id: string; blockers: Blocker[]; onNavigate: (blocker: Blocker) => void }) {
  if (!blockers.length) return null;
  return <aside id={id} className="notice action-requirements" aria-label="Причины блокировки действия">
    <strong>До подтверждения осталось: {blockers.length}</strong>
    <BlockerList blockers={blockers.slice(0, 3)} onNavigate={onNavigate} />
    {blockers.length > 3 && <details><summary>Показать остальные причины · {blockers.length - 3}</summary><BlockerList blockers={blockers.slice(3)} onNavigate={onNavigate} /></details>}
  </aside>;
}

// Navigation only: every eligibility check and final decision stays on the server.
export function BlockerList({ blockers, onNavigate }: { blockers: Blocker[]; onNavigate: (blocker: Blocker) => void }) {
  return <ul className="action-blockers">{blockers.map((blocker, index) => <li key={blocker.code + ':' + blocker.item_code + ':' + index}>
    {blockerTarget(blocker) ? <button type="button" className="blocker-link" onClick={() => onNavigate(blocker)}>{blocker.message}</button> : blocker.message}
    {blockerHint(blocker.code) && <p>{blockerHint(blocker.code)}</p>}
  </li>)}</ul>;
}
