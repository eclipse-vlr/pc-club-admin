import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, APIError } from './api';
import type { Identity, User } from './api';
import { parseRubles, stringifyExact } from './money';
import { CashPanel } from './CashPanel';
import type { CashView } from './CashPanel';
import { HandoverPanel } from './HandoverPanel';
import { ExcelSyncPanel } from './ExcelSyncPanel';
import { confirmChecklistDiscard } from './checklist-drafts';

type Entry = { code: string; phase: string; label: string; help_text: string; answer: string; comment: string; equipment_check: boolean; equipment_issue: boolean; answered_by: string | null; version: number };
type Note = { id: string; checklist_code: string; category: string; object_label: string; text: string; version: number };
type Shift = { handover_id:string;handover_parent_id:string; id: string; owner_id: string; status: string; kind: string; business_date: string; template_version: number; version: number; planned_start_at: string; planned_end_at: string; checklist: Entry[]; notes: Note[]; cash: CashView | null };
type Blocker = { code: string; item_code?: string; message: string };
type Progress = { completed: number; total: number; blockers: Blocker[] };
type Readiness = { acceptance: Progress; closing: Progress; blockers: Blocker[]; can_activate: boolean; can_prepare_handover: boolean };

export function ChecklistPanel({ identity, users,excelSyncEnabled,onSessionEnd }: { identity: Identity; users: User[];excelSyncEnabled:boolean;onSessionEnd:()=>Promise<void> }) {
  const [shift, setShift] = useState<Shift | null>(null);
  const [ready, setReady] = useState<Readiness | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [generation, setGeneration] = useState(0);
  const dirty = useRef(new Set<string>());
  const [dirtyCount, setDirtyCount] = useState(0);
  const loadRequest = useRef(0);
  function markDirty(code: string, changed: boolean) {
    if (changed) dirty.current.add(code); else dirty.current.delete(code);
    setDirtyCount(dirty.current.size);
  }
  function focusEntry(code: string) {
    const form = document.getElementById('checklist-' + code);
    if (!form) return;
    form.scrollIntoView({ block: 'center' });
    const control = form.querySelector<HTMLElement>('select:not(:disabled), textarea:not(:disabled), input:not(:disabled), button:not(:disabled)');
    (control ?? form).focus({ preventScroll: true });
  }
  const attempt = useRef<{ body: string; key: string } | null>(null);
  const actor = identity.user.id;
  const manager = identity.user.roles.includes('manager');
  const editable = shift?.owner_id === actor && identity.pin_session && ['opening', 'active'].includes(shift.status);
  const message = (cause: unknown) => cause instanceof APIError ? cause.message : 'Связь прервалась. Перечитайте сохранённые данные перед повтором.';

  async function apply(result: Shift, current = () => true) {
    const readiness = await api<Readiness>('/shifts/' + result.id + '/readiness');
    if (current()) { setShift(result); setReady(readiness); }
  }
  async function load() {
    const request = ++loadRequest.current;
    const current = () => loadRequest.current === request;
    setBusy(true); setError(''); setReady(null);
    try {
      const result = await api<{ shift: Shift | null }>('/shifts/current');
      if (!current()) return;
      if (result.shift) await apply(result.shift, current);
      else setShift(null);
      if (!current()) return;
      dirty.current.clear(); setDirtyCount(0);
      setGeneration(value => value + 1); setLoaded(true);
    } catch (cause) { if (current()) setError(message(cause)); throw cause; }
    finally { if (current()) setBusy(false); }
  }
  useEffect(() => {
    void load().catch(() => {});
    return () => { loadRequest.current++; };
  }, []);

  async function bootstrap(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    const amountText = String(data.get('cash') ?? '');
    let amount: bigint;
    try { amount = parseRubles(amountText); if (amount < 0n) throw new Error('Начальный остаток не может быть отрицательным'); } catch (cause) { setError(cause instanceof Error ? cause.message : 'Проверьте сумму'); return; }
    const body = { owner_id: String(data.get('owner')), business_date: String(data.get('date')), kind: String(data.get('kind')), opening_cash_kopecks: amount, reason: String(data.get('reason')) };
    const canonical = stringifyExact(body);
    if (attempt.current?.body !== canonical) attempt.current = { body: canonical, key: crypto.randomUUID() };
    const key = attempt.current.key;
    setBusy(true); setError('');
    try { const result = await api<Shift>('/shifts/bootstrap', 'POST', body, actor, key); await apply(result); form.reset(); }
    catch (cause) { setError(message(cause)); }
    finally { setBusy(false); }
  }
  async function saveEntry(entry: Entry, data: FormData, version: number): Promise<Entry> {
    if (!shift) throw new Error('Смена недоступна. Перечитайте данные.');
    setBusy(true); setError('');
    try {
      const result = await api<Shift>('/shifts/' + shift.id + '/checklist/' + entry.code, 'PATCH', { answer: String(data.get('answer')), comment: String(data.get('comment') ?? ''), equipment_issue: data.get('issue') === 'on', version }, actor);
      await apply(result);
      const saved = result.checklist.find(item => item.code === entry.code);
      if (!saved) throw new Error('Сохранённый ответ не получен. Перечитайте данные.');
      markDirty(entry.code, false);
      return saved;
    } catch (cause) { setError(message(cause)); throw cause; }
    finally { setBusy(false); }
  }
  async function saveNote(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!shift) return;
    const form = event.currentTarget; const data = new FormData(form);
    setBusy(true); setError('');
    try {
      await api('/shifts/' + shift.id + '/notes', 'POST', { checklist_code: String(data.get('code') ?? ''), category: String(data.get('category')), object_label: String(data.get('object') ?? ''), text: String(data.get('text')) }, actor);
      const result = await api<Shift>('/shifts/' + shift.id); await apply(result); form.reset(); markDirty('note', false);
    } catch (cause) { setError(message(cause)); }
    finally { setBusy(false); }
  }
  return <section aria-label="Текущая смена">
    <h2>Смена и чек-лист</h2>
    <p>Черновик не назначает ответственного. В тестовом режиме первый приём требует чек-листа, собственного пересчёта и свежего PIN. Для передачи оба сотрудника лично входят по PIN на одном браузере.</p>
    <button disabled={busy} onClick={() => { if (confirmChecklistDiscard()) void load().catch(() => {}); }}>Перечитать сохранённые данные</button>
    {dirtyCount > 0 && <p className="unsaved-notice" role="status">Есть несохранённые ответы или замечание. Сохраните их перед приёмом, передачей или кассовыми действиями. Прогресс учитывает только сохранённые данные.</p>}
    {error && <p role="alert" className="error">{error}</p>}
    {!loaded ? <p>{error ? 'Данные смены пока не получены. Повторите загрузку.' : 'Загружаем черновик…'}</p> : !shift ? <>
      <p>Доступной вам смены или черновика нет.</p>
      {manager && <form onSubmit={event => void bootstrap(event)}>
        <h3>Подготовить первую смену · Только тестовые данные</h3>
        <label>Ответственный администратор<select name="owner" required defaultValue=""><option value="" disabled>Выберите сотрудника</option>{users.filter(user => user.active && user.roles.includes('admin')).map(user => <option key={user.id} value={user.id}>{user.display_name}</option>)}</select></label>
        <label>Дата начала в часовом поясе клуба<input type="date" name="date" required /></label>
        <label>Смена<select name="kind"><option value="day">Дневная 09:00–21:00</option><option value="night">Ночная 21:00–09:00</option></select></label>
        <label>Первоначальный пересчёт · рубли<input name="cash" inputMode="numeric" required /></label>
        <p>Ноль нужно ввести явно. Это исходная сумма управляющего, ещё не принятый сотрудником остаток.</p>
        <label>Основание первоначальной суммы<textarea name="reason" required maxLength={4000} /></label>
        <button disabled={busy}>Создать черновик смены</button>
      </form>}
    </> : <>
      <div className="shift-overview"><div><strong>{shift.business_date}</strong><p>{shift.kind === 'night' ? 'Ночная 21:00–09:00' : 'Дневная 09:00–21:00'}</p><p>Ответственный: {users.find(user => user.id === shift.owner_id)?.display_name ?? (shift.owner_id === actor ? identity.user.display_name : 'Назначенный сотрудник')}</p></div><span className="shift-state">{shift.status === 'opening' ? 'Черновик приёма' : shift.status === 'active' ? 'Активная смена' : shift.status==='handover_pending'?'Ожидает приёма · ответственность сохраняется':'Изменения недоступны'}</span></div>
      <ExcelSyncPanel key={shift.id} shiftID={shift.id} version={shift.version} enabled={excelSyncEnabled} />
      {!editable && <p>Просмотр без права изменения. Ответы заполняет назначенный администратор после личного PIN-входа.</p>}
      {ready && <div className="progress-grid" aria-label="Сохранённый прогресс чек-листов">{(['acceptance', 'closing'] as const).map(phase => <div className="progress-card" key={phase}><h3>{phase === 'acceptance' ? 'Приём смены' : 'Сдача смены'}<span>{ready[phase].completed} / {ready[phase].total}</span></h3><progress max={ready[phase].total || 1} value={ready[phase].completed} aria-label={phase === 'acceptance' ? 'Прогресс приёма' : 'Прогресс сдачи'} /><p>{ready[phase].blockers.length ? `Причин, требующих внимания: ${ready[phase].blockers.length}` : 'Обязательные пункты заполнены'}</p></div>)}</div>}
      <aside className="readiness-status">{ready ? ready.blockers.length ? <details><summary>Что осталось проверить: {ready.blockers.length}</summary><ul>{ready.blockers.map((blocker, index) => <li key={blocker.code + ':' + index}>{blocker.item_code ? <button type="button" className="blocker-link" onClick={() => { if (blocker.item_code) focusEntry(blocker.item_code); }}>{blocker.message}</button> : blocker.message}</li>)}</ul></details> : <p>По сохранённым данным блокирующих причин нет. Для ответственного действия потребуется личное подтверждение.</p> : <p>Проверка готовности ещё не получена.</p>}</aside>
      {(['acceptance', 'closing'] as const).map(phase => <div className="checklist-phase" key={phase}>
        <h3>{phase === 'acceptance' ? 'Приём' : 'Сдача'}: {ready ? `${ready[phase].completed} из ${ready[phase].total}` : 'Проверяем…'}</h3>
        <button type="button" className="checklist-jump" disabled={busy || !ready?.[phase].blockers.some(blocker => blocker.item_code)} onClick={() => { const code = ready?.[phase].blockers.find(blocker => blocker.item_code)?.item_code; if (code) focusEntry(code); }}>К первому незавершённому пункту · {phase === 'acceptance' ? 'приём' : 'сдача'}</button>
        <div className="checklist-grid">{shift.checklist.filter(entry => entry.phase === phase).map(entry => <EntryEditor key={generation + ':' + entry.code} entry={entry} disabled={busy || !editable || (phase === 'acceptance' ? shift.status !== 'opening' : shift.status !== 'active')} onDirty={changed => markDirty(entry.code, changed)} onSave={(data, version) => saveEntry(entry, data, version)} />)}</div>
      </div>)}
      <fieldset className="shift-actions" disabled={dirtyCount > 0}><legend>Приём, касса и передача</legend>
      {!(shift.status==='opening'&&shift.handover_parent_id)&&<CashPanel key={"cash:" + generation + ":" + shift.id + ":" + shift.version} identity={identity} shift={shift} readyToActivate={ready?.can_activate === true} refresh={load} />}
      <HandoverPanel key={"handover:"+generation+":"+shift.id+":"+shift.version} identity={identity} shift={shift} users={users} canPrepare={ready?.can_prepare_handover===true} refresh={load} onSessionEnd={onSessionEnd}/>
      </fieldset>
      <h3>Замечания</h3>
      {shift.notes.length ? <ul>{shift.notes.map(note => <li key={note.id}><strong>{note.category === 'equipment' ? 'Неисправность' : note.category === 'conflict' ? 'Конфликт' : 'Заметка'}</strong> {note.object_label}: {note.text}</li>)}</ul> : <p>Замечаний пока нет. Это не означает, что оборудование уже проверено.</p>}
      {editable && <form data-checklist-dirty={dirty.current.has('note') ? 'true' : undefined} onChange={event => { const data = new FormData(event.currentTarget); markDirty('note', !!data.get('text') || !!data.get('object') || !!data.get('code') || data.get('category') !== 'general'); }} onSubmit={event => void saveNote(event)}>
        <label>Пункт<select name="code" defaultValue=""><option value="">Общая заметка</option>{shift.checklist.filter(entry => shift.status === 'opening' ? entry.phase === 'acceptance' : entry.phase === 'closing').map(entry => <option key={entry.code} value={entry.code}>{entry.label}</option>)}</select></label>
        <label>Категория<select name="category"><option value="general">Общая заметка</option><option value="equipment">Неисправность</option><option value="conflict">Конфликт</option></select></label>
        <label>Объект / место · обязательно для поломки<input name="object" maxLength={200} /></label>
        <label>Описание<textarea name="text" required maxLength={4000} /></label>
        <p>Для поломки выберите пункт проверки оборудования. Для конфликта — пункт конфликтных ситуаций. Ремонт до передачи не обязателен.</p>
        <button disabled={busy}>Сохранить замечание</button>
      </form>}
    </>}
  </section>;
}
function EntryEditor({ entry, disabled, onSave, onDirty }: { entry: Entry; disabled: boolean; onSave: (data: FormData, version: number) => Promise<Entry>; onDirty: (changed: boolean) => void }) {
  const [baseline, setBaseline] = useState(entry);
  const [draft, setDraft] = useState(entry);
  const [state, setState] = useState(entry.answered_by ? 'Сохранено' : 'Не заполнено');
  const [uncertain, setUncertain] = useState(false);
  const changed = draft.answer !== baseline.answer || draft.comment !== baseline.comment || draft.equipment_issue !== baseline.equipment_issue;
  useEffect(() => {
    // Preserve drafts and their original optimistic-lock version across other saves.
    if (!changed && !uncertain) { setBaseline(entry); setDraft(entry); setState(entry.answered_by ? 'Сохранено' : 'Не заполнено'); }
  }, [entry, changed, uncertain]);
  function edit(next: Entry) {
    const different = next.answer !== baseline.answer || next.comment !== baseline.comment || next.equipment_issue !== baseline.equipment_issue;
    setDraft(next); onDirty(different); setState(different ? 'Не сохранено' : baseline.answered_by ? 'Сохранено' : 'Не заполнено');
  }
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const data = new FormData(event.currentTarget); setState('Сохраняется…');
    try { const saved = await onSave(data, baseline.version); setBaseline(saved); setDraft(saved); onDirty(false); setState('Сохранено'); }
    catch { setState('Ошибка. Перечитайте данные перед повтором.'); setUncertain(true); }
  }
  return <form id={'checklist-' + entry.code} tabIndex={-1} aria-label={entry.label} className="checklist-item" data-checklist-dirty={changed ? 'true' : undefined} onSubmit={event => void save(event)}>
    <fieldset disabled={disabled || uncertain}>
      <legend>{entry.label}</legend>
      {entry.help_text && <p>{entry.help_text}</p>}
      {changed && entry.version !== baseline.version && <p className="draft-conflict">Этот пункт изменился на сервере. Перечитайте данные перед повтором.</p>}
      <label>Ответ<select name="answer" value={draft.answer} onChange={event => edit({ ...draft, answer: event.target.value })}><option value="unanswered">Не отвечено</option><option value="yes">Да</option><option value="no">{entry.code === 'conflicts_recorded' ? 'Нет конфликтов' : 'Нет — пункт не выполнен'}</option></select></label>
      {entry.equipment_check && <label><input className="inline" name="issue" type="checkbox" checked={draft.equipment_issue} onChange={event => edit({ ...draft, equipment_issue: event.target.checked })} /> Обнаружена неисправность — требуется связанная заметка</label>}
      <label>Комментарий<textarea name="comment" maxLength={4000} value={draft.comment} onChange={event => edit({ ...draft, comment: event.target.value })} /></label>
      <button>Сохранить ответ</button><span role="status">{state}</span>
    </fieldset>
  </form>;
}
