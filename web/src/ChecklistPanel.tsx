import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, APIError } from './api';
import type { Identity, User } from './api';
import { parseRubles, stringifyExact } from './money';
import { CashPanel } from './CashPanel';
import type { CashView } from './CashPanel';
import { HandoverPanel } from './HandoverPanel';
import { ExcelSyncPanel } from './ExcelSyncPanel';
import { confirmDraftDiscard } from './checklist-drafts';
import { BlockerList, blockerTarget } from './BlockerList';
import type { Blocker } from './BlockerList';

type Entry = { code: string; phase: string; label: string; help_text: string; answer: string; comment: string; equipment_check: boolean; equipment_issue: boolean; answered_by: string | null; version: number };
type Note = { id: string; checklist_code: string; category: string; object_label: string; text: string; version: number };
type Shift = { handover_id:string;handover_parent_id:string; id: string; owner_id: string; status: string; kind: string; business_date: string; template_version: number; version: number; planned_start_at: string; planned_end_at: string; checklist: Entry[]; notes: Note[]; cash: CashView | null };
type Progress = { completed: number; total: number; blockers: Blocker[] };
type Readiness = { version: number; acceptance: Progress; closing: Progress; blockers: Blocker[]; can_activate: boolean; can_prepare_handover: boolean };

export function ChecklistPanel({ identity, users,excelSyncEnabled,onSessionEnd }: { identity: Identity; users: User[];excelSyncEnabled:boolean;onSessionEnd:()=>Promise<Identity | null> }) {
  const [shift, setShift] = useState<Shift | null>(null);
  const [ready, setReady] = useState<Readiness | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [generation, setGeneration] = useState(0);
  const [storageBusy, setStorageBusy] = useState(false);
  const [storageUncertain, setStorageUncertain] = useState<'unknown' | 'refresh-required' | null>(null);
  const completedVersions = useRef(new Map<string, number>());
  const lastProgress = useRef<{ id: string; value: Readiness } | null>(null);
  const dirty = useRef(new Set<string>());
  const [dirtyCount, setDirtyCount] = useState(0);
  const [cashDirty, setCashDirty] = useState(false);
  const loadRequest = useRef(0);
  function markDirty(code: string, changed: boolean) {
    if (changed) dirty.current.add(code); else dirty.current.delete(code);
    setDirtyCount(dirty.current.size);
  }
  function focusEntry(code: string) {
    const form = document.getElementById('checklist-' + code);
    if (!form) return;
    reveal(form);
    form.scrollIntoView({ block: 'center' });
    const control = form.querySelector<HTMLElement>('select:not(:disabled), textarea:not(:disabled), input:not(:disabled), button:not(:disabled)');
    (control ?? form).focus({ preventScroll: true });
  }
  function focusSection(id: string) {
    const target = document.getElementById(id);
    if (!target) return;
    reveal(target);
    target.scrollIntoView({ block: 'center' });
    const control = target.matches('details') ? target.querySelector<HTMLElement>('summary') : target.matches('button, input, select, textarea') ? target : target.querySelector<HTMLElement>('input:not(:disabled), select:not(:disabled), textarea:not(:disabled), button:not(:disabled)');
    (control ?? target).focus({ preventScroll: true });
  }
  function navigateBlocker(blocker: Blocker) {
    if (blocker.item_code && blocker.code !== 'NOTE_REQUIRED') focusEntry(blocker.item_code);
    else { const target = blockerTarget(blocker); if (target) focusSection(target); }
  }
  const attempt = useRef<{ body: string; key: string } | null>(null);
  const actor = identity.user.id;
  const manager = identity.user.roles.includes('manager');
  const editable = shift?.owner_id === actor && identity.pin_session && ['opening', 'active'].includes(shift.status);
  const message = (cause: unknown) => cause instanceof APIError ? cause.message : 'Связь прервалась. Перечитайте сохранённые данные перед повтором.';

  async function apply(result: Shift, current = () => true) {
    if (!current()) return;
    setShift(result); setReady(null);
    const readiness = await api<Readiness>('/shifts/' + result.id + '/readiness');
    if (current()) {
      if (readiness.version !== result.version) throw new APIError('VERSION_CONFLICT', 'Смена изменилась в другом окне. Перечитайте сохранённые данные.', 409);
      completedVersions.current.clear();
      for (const entry of result.checklist) {
        const progress = entry.phase === 'acceptance' ? readiness.acceptance : readiness.closing;
        if (entry.answered_by && !progress.blockers.some(blocker => blocker.item_code === entry.code)) completedVersions.current.set(result.id + ':' + entry.code, entry.version);
      }
      lastProgress.current = { id: result.id, value: readiness };
      setReady(readiness);
    }
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
      dirty.current.clear(); setDirtyCount(0); setStorageUncertain(null);
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
    setBusy(true); setStorageBusy(true); setError('');
    let confirmed = false;
    try {
      const result = await api<Shift>('/shifts/' + shift.id + '/checklist/' + entry.code, 'PATCH', { answer: String(data.get('answer')), comment: String(data.get('comment') ?? ''), equipment_issue: data.get('issue') === 'on', version }, actor);
      const saved = result.checklist.find(item => item.code === entry.code);
      if (!saved) throw new Error('Сохранённый ответ не получен. Перечитайте данные.');
      confirmed = true;
      try { await apply(result); }
      catch { setError('Ответ сохранён в приложении. Не удалось проверить готовность смены — перечитайте сохранённые данные перед передачей.'); }
      markDirty(entry.code, false);
      return saved;
    } catch (cause) {
      if (!confirmed && !(cause instanceof APIError && cause.status !== undefined && cause.status >= 400 && cause.status < 500)) setStorageUncertain('unknown');
      setError(message(cause)); throw cause;
    }
    finally { setBusy(false); setStorageBusy(false); }
  }
  async function saveNote(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!shift || storageUncertain) return;
    const form = event.currentTarget; const data = new FormData(form);
    setBusy(true); setStorageBusy(true); setError('');
    let confirmed = false;
    try {
      await api('/shifts/' + shift.id + '/notes', 'POST', { checklist_code: String(data.get('code') ?? ''), category: String(data.get('category')), object_label: String(data.get('object') ?? ''), text: String(data.get('text')) }, actor);
      confirmed = true; form.reset(); markDirty('note', false);
      const result = await api<Shift>('/shifts/' + shift.id); await apply(result);
    } catch (cause) {
      if (confirmed) { setStorageUncertain('refresh-required'); setError('Заметка сохранена в приложении. Перечитайте данные перед новой операцией.'); }
      else {
        if (!(cause instanceof APIError && cause.status !== undefined && cause.status >= 400 && cause.status < 500)) setStorageUncertain('unknown');
        setError(message(cause));
      }
    }
    finally { setBusy(false); setStorageBusy(false); }
  }
  const localBlockers: Blocker[] = [];
  for (const code of dirty.current) localBlockers.push(code === 'note'
    ? { code: 'UNSAVED_NOTE', message: 'Сохраните несохранённое замечание.' }
    : { code: 'UNSAVED_ANSWER', item_code: code, message: 'Сохраните ответ: ' + (shift?.checklist.find(item => item.code === code)?.label ?? code) });
  if (storageUncertain) localBlockers.unshift({ code: 'STORAGE_UNKNOWN', message: storageUncertain === 'refresh-required' ? 'Запись сохранена. Перечитайте актуальные данные смены перед новой операцией.' : 'Результат сохранения неизвестен. Перечитайте данные перед новой операцией.' });
  if (storageBusy) localBlockers.unshift({ code: 'STORAGE_SAVING', message: 'Дождитесь сохранения в приложении.' });
  const actionBlockers: Blocker[] = [...localBlockers, ...(cashDirty ? [{ code: 'UNSAVED_CASH', message: 'Сохраните несохранённые данные кассы.' }] : []), ...(ready?.blockers ?? [{ code: 'READINESS_UNKNOWN', message: 'Готовность смены не получена. Перечитайте сохранённые данные.' }])];
  const draftsPending = dirtyCount > 0 || storageBusy || !!storageUncertain;
  const progress = ready ?? (lastProgress.current?.id === shift?.id ? lastProgress.current?.value : null);
  return <section aria-label="Текущая смена">
    <h2>Смена и чек-лист</h2>
    <p>Черновик не назначает ответственного. В тестовом режиме первый приём требует чек-листа, собственного пересчёта и свежего PIN. Для передачи оба сотрудника лично входят по PIN на одном браузере.</p>
    <button id="shift-reload" disabled={busy} onClick={() => { if (confirmDraftDiscard()) void load().catch(() => {}); }}>Перечитать сохранённые данные</button>
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
      <ExcelSyncPanel key={shift.id} shiftID={shift.id} version={shift.version} enabled={excelSyncEnabled} unsavedCount={dirtyCount} saving={storageBusy} saveUncertain={storageUncertain === 'unknown'} needsStorageRead={storageUncertain === 'refresh-required'} />
      {!editable && <p>Просмотр без права изменения. Ответы заполняет назначенный администратор после личного PIN-входа.</p>}
      {progress && <div className="progress-grid" aria-label="Сохранённый прогресс чек-листов">{(['acceptance', 'closing'] as const).map(phase => <div className="progress-card" key={phase}><h3>{phase === 'acceptance' ? 'Приём смены' : 'Сдача смены'}<span>{progress[phase].completed} / {progress[phase].total}</span></h3><progress max={progress[phase].total || 1} value={progress[phase].completed} aria-label={phase === 'acceptance' ? 'Прогресс приёма' : 'Прогресс сдачи'} /><p>{!ready ? 'Обновляем проверку готовности…' : progress[phase].blockers.length ? `Причин, требующих внимания: ${progress[phase].blockers.length}` : 'Обязательные пункты заполнены'}</p></div>)}</div>}
      <nav className="shift-shortcuts" aria-label="Действия со сменой"><a href="#shift-acceptance" onClick={() => focusSection('shift-acceptance')}>Приём</a><a href="#shift-closing" onClick={() => focusSection('shift-closing')}>Сдача</a><a href="#shift-cash">Касса</a><a href="#shift-handover">Передача</a><a href="#shift-notes">Замечания</a></nav>
      <aside className="readiness-status">{ready ? ready.blockers.length ? <details><summary>Что осталось проверить: {ready.blockers.length}</summary><BlockerList blockers={ready.blockers} onNavigate={navigateBlocker} /></details> : <p>По сохранённым данным блокирующих причин нет. Для ответственного действия потребуется личное подтверждение.</p> : <p>Проверка готовности ещё не получена.</p>}</aside>
      {(['acceptance', 'closing'] as const).map(phase => <details className="checklist-phase" key={phase} id={'shift-' + phase} tabIndex={-1} open={phase === (shift.status === 'opening' ? 'acceptance' : 'closing')}>
        <summary><h3>{phase === 'acceptance' ? 'Приём' : 'Сдача'}: {progress ? `${progress[phase].completed} из ${progress[phase].total}` : 'Проверяем…'}</h3><span>Раскрыть / свернуть этап</span></summary>
        <button type="button" className="checklist-jump" disabled={busy || !ready?.[phase].blockers.some(blocker => blocker.item_code)} onClick={() => { const code = ready?.[phase].blockers.find(blocker => blocker.item_code)?.item_code; if (code) focusEntry(code); }}>К первому незавершённому пункту · {phase === 'acceptance' ? 'приём' : 'сдача'}</button>
        <div className="checklist-grid">{shift.checklist.filter(entry => entry.phase === phase).map(entry => <EntryEditor complete={completedVersions.current.get(shift.id + ':' + entry.code) === entry.version} key={generation + ':' + entry.code} entry={entry} disabled={busy || !!storageUncertain || !editable || (phase === 'acceptance' ? shift.status !== 'opening' : shift.status !== 'active')} onDirty={changed => markDirty(entry.code, changed)} onSave={(data, version) => saveEntry(entry, data, version)} />)}</div>
      </details>)}
      <fieldset className="shift-actions"><legend>Приём, касса и передача</legend>
      {!(shift.status==='opening'&&shift.handover_parent_id)&&<CashPanel key={"cash:" + generation + ":" + shift.id} identity={identity} shift={shift} onDirty={setCashDirty} draftsPending={draftsPending || busy} activationBlockers={[...localBlockers, ...(ready?.acceptance.blockers ?? [{ code: 'READINESS_UNKNOWN', message: 'Готовность приёма не получена. Перечитайте сохранённые данные.' }])]} onNavigate={navigateBlocker} readyToActivate={ready?.can_activate === true && !busy} refresh={load} />}
      <HandoverPanel key={"handover:"+generation+":"+shift.id+":"+shift.version} identity={identity} shift={shift} users={users} draftsPending={draftsPending || cashDirty} readinessKnown={!!ready} readinessBlockers={actionBlockers} acceptanceBlockers={ready?.acceptance.blockers ?? []} onNavigate={navigateBlocker} canPrepare={ready?.can_prepare_handover===true && !busy} checklistReady={!!ready && ready.acceptance.total > 0 && ready.acceptance.blockers.length === 0 && ready.acceptance.completed === ready.acceptance.total} refresh={load} onSessionEnd={onSessionEnd}/>
      </fieldset>
      <div id="shift-notes" tabIndex={-1}><h3>Замечания</h3>
      {shift.notes.length ? <ul>{shift.notes.map(note => <li key={note.id}><strong>{note.category === 'equipment' ? 'Неисправность' : note.category === 'conflict' ? 'Конфликт' : 'Заметка'}</strong> {note.object_label}: {note.text}</li>)}</ul> : <p>Замечаний пока нет. Это не означает, что оборудование уже проверено.</p>}
      {editable && <form data-checklist-dirty={dirty.current.has('note') ? 'true' : undefined} onChange={event => { const data = new FormData(event.currentTarget); markDirty('note', !!data.get('text') || !!data.get('object') || !!data.get('code') || data.get('category') !== 'general'); }} onSubmit={event => void saveNote(event)}>
        <label>Пункт<select name="code" defaultValue=""><option value="">Общая заметка</option>{shift.checklist.filter(entry => shift.status === 'opening' ? entry.phase === 'acceptance' : entry.phase === 'closing').map(entry => <option key={entry.code} value={entry.code}>{entry.label}</option>)}</select></label>
        <label>Категория<select name="category"><option value="general">Общая заметка</option><option value="equipment">Неисправность</option><option value="conflict">Конфликт</option></select></label>
        <label>Объект / место · обязательно для поломки<input name="object" maxLength={200} /></label>
        <label>Описание<textarea name="text" required maxLength={4000} /></label>
        <p>Для поломки выберите пункт проверки оборудования. Для конфликта — пункт конфликтных ситуаций. Ремонт до передачи не обязателен.</p>
        <button disabled={busy || !!storageUncertain}>Сохранить замечание</button>
      </form>}
      </div>
    </>}
  </section>;
}
function EntryEditor({ entry, disabled, complete, onSave, onDirty }: { entry: Entry; disabled: boolean; complete: boolean; onSave: (data: FormData, version: number) => Promise<Entry>; onDirty: (changed: boolean) => void }) {
  const summary = useRef<HTMLElement>(null);
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
    try { const saved = await onSave(data, baseline.version); setBaseline(saved); setDraft(saved); onDirty(false); setState('Сохранено'); summary.current?.focus(); }
    catch { setState('Ошибка. Перечитайте данные перед повтором.'); setUncertain(true); }
  }
  return <form id={'checklist-' + entry.code} tabIndex={-1} aria-label={entry.label} className="checklist-item" data-complete={complete && !uncertain && !changed} data-checklist-dirty={changed ? 'true' : undefined} onSubmit={event => void save(event)}>
    <details open={!complete || changed || uncertain}>
    <summary ref={summary}><span>{entry.label}</span><span role="status">{state}</span></summary>
    <fieldset disabled={disabled || uncertain}>
      <legend className="visually-hidden">{entry.label}</legend>
      {entry.help_text && <p>{entry.help_text}</p>}
      {changed && entry.version !== baseline.version && <p className="draft-conflict">Этот пункт изменился на сервере. Перечитайте данные перед повтором.</p>}
      <label>Ответ<select name="answer" value={draft.answer} onChange={event => edit({ ...draft, answer: event.target.value })}><option value="unanswered">Не отвечено</option><option value="yes">Да</option><option value="no">{entry.code === 'conflicts_recorded' ? 'Нет конфликтов' : 'Нет — пункт не выполнен'}</option></select></label>
      {entry.equipment_check && <label><input className="inline" name="issue" type="checkbox" checked={draft.equipment_issue} onChange={event => edit({ ...draft, equipment_issue: event.target.checked })} /> Обнаружена неисправность — требуется связанная заметка</label>}
      <label>Комментарий<textarea name="comment" maxLength={4000} value={draft.comment} onChange={event => edit({ ...draft, comment: event.target.value })} /></label>
      <button>Сохранить ответ</button>
    </fieldset>
    </details>
  </form>;
}

function reveal(target: HTMLElement) {
  for (let node: HTMLElement | null = target; node; node = node.parentElement) {
    if (node instanceof HTMLDetailsElement) node.open = true;
  }
  if (target.matches('form.checklist-item')) { const details = target.querySelector('details'); if (details) details.open = true; }
}
