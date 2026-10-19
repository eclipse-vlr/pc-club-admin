import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, APIError } from './api';
import type { Identity, User } from './api';
import { parseRubles, stringifyExact } from './money';
import { CashPanel } from './CashPanel';
import type { CashView } from './CashPanel';
import { ExcelSyncPanel } from './ExcelSyncPanel';

type Entry = { code: string; phase: string; label: string; help_text: string; answer: string; comment: string; equipment_check: boolean; equipment_issue: boolean; answered_by: string | null; version: number };
type Note = { id: string; checklist_code: string; category: string; object_label: string; text: string; version: number };
type Shift = { id: string; owner_id: string; status: string; kind: string; business_date: string; template_version: number; version: number; planned_start_at: string; planned_end_at: string; checklist: Entry[]; notes: Note[]; cash: CashView | null };
type Blocker = { code: string; item_code?: string; message: string };
type Progress = { completed: number; total: number; blockers: Blocker[] };
type Readiness = { acceptance: Progress; closing: Progress; blockers: Blocker[]; can_activate: boolean; can_prepare_handover: boolean };

export function ChecklistPanel({ identity, users,excelSyncEnabled }: { identity: Identity; users: User[];excelSyncEnabled:boolean }) {
  const [shift, setShift] = useState<Shift | null>(null);
  const [ready, setReady] = useState<Readiness | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [generation, setGeneration] = useState(0);
  const attempt = useRef<{ body: string; key: string } | null>(null);
  const actor = identity.user.id;
  const manager = identity.user.roles.includes('manager');
  const editable = shift?.owner_id === actor && identity.pin_session && ['opening', 'active'].includes(shift.status);
  const message = (cause: unknown) => cause instanceof APIError ? cause.message : 'Связь прервалась. Перечитайте сохранённые данные перед повтором.';

  async function apply(result: Shift) {
    const readiness = await api<Readiness>('/shifts/' + result.id + '/readiness');
    setShift(result);
    setReady(readiness);
  }
  async function load() {
    setError(''); setReady(null);
    try {
      const result = await api<{ shift: Shift | null }>('/shifts/current');
      if (result.shift) await apply(result.shift);
      else setShift(null);
      setGeneration(value => value + 1);
      setLoaded(true);
    // Propagate failure so the cash form keeps its uncertainty lock.
    } catch (cause) { setError(message(cause)); throw cause; }
  }
  useEffect(() => { void load().catch(() => {}); }, []);

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
  async function saveEntry(entry: Entry, data: FormData) {
    if (!shift) return;
    setBusy(true); setError('');
    try {
      const result = await api<Shift>('/shifts/' + shift.id + '/checklist/' + entry.code, 'PATCH', { answer: String(data.get('answer')), comment: String(data.get('comment') ?? ''), equipment_issue: data.get('issue') === 'on', version: entry.version }, actor);
      await apply(result);
    } catch (cause) { setError(message(cause)); throw cause; }
    finally { setBusy(false); }
  }
  async function saveNote(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!shift) return;
    const form = event.currentTarget; const data = new FormData(form);
    setBusy(true); setError('');
    try {
      await api('/shifts/' + shift.id + '/notes', 'POST', { checklist_code: String(data.get('code') ?? ''), category: String(data.get('category')), object_label: String(data.get('object') ?? ''), text: String(data.get('text')) }, actor);
      const result = await api<Shift>('/shifts/' + shift.id); await apply(result); form.reset();
    } catch (cause) { setError(message(cause)); }
    finally { setBusy(false); }
  }
  return <section>
    <h2>Первый черновик смены и чек-лист</h2>
    <p>Черновик не назначает ответственного. В тестовом режиме первый приём требует чек-листа, собственного пересчёта и свежего PIN. Передача следующему сотруднику пока недоступна.</p>
    <button disabled={busy} onClick={() => void load().catch(() => {})}>Перечитать сохранённые данные</button>
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
        <button disabled={busy}>Создать opening-черновик</button>
      </form>}
    </> : <>
      <p>{shift.business_date} · {shift.kind === 'night' ? 'Ночная 21:00–09:00' : 'Дневная 09:00–21:00'} · {shift.status === 'opening' ? 'Черновик приёма' : shift.status === 'active' ? 'Активная смена' : 'Изменения недоступны'} · Шаблон {shift.template_version}</p>
      <ExcelSyncPanel key={shift.id} shiftID={shift.id} version={shift.version} enabled={excelSyncEnabled} />
      {!editable && <p>Просмотр без права изменения. Ответы заполняет назначенный администратор после личного PIN-входа.</p>}
      {(['acceptance', 'closing'] as const).map(phase => <div key={phase}>
        <h3>{phase === 'acceptance' ? 'Приём' : 'Сдача'}: {ready ? `${ready[phase].completed} из ${ready[phase].total}` : 'Проверяем…'}</h3>
        {shift.checklist.filter(entry => entry.phase === phase).map(entry => <EntryEditor key={generation + ':' + entry.code + ':' + entry.version} entry={entry} disabled={busy || !editable || (phase === 'acceptance' ? shift.status !== 'opening' : shift.status !== 'active')} onSave={data => saveEntry(entry, data)} />)}
      </div>)}
      <CashPanel key={generation + ":" + shift.id + ":" + shift.version} identity={identity} shift={shift} readyToActivate={ready?.can_activate === true} refresh={load} />
      <h3>Блокирующие причины</h3>
      {ready ? <ul>{ready.blockers.map((blocker, index) => <li key={blocker.code + ':' + index}>{blocker.message}</li>)}</ul> : <p>Проверка ещё не получена.</p>}
      <h3>Замечания</h3>
      {shift.notes.length ? <ul>{shift.notes.map(note => <li key={note.id}><strong>{note.category === 'equipment' ? 'Неисправность' : note.category === 'conflict' ? 'Конфликт' : 'Заметка'}</strong> {note.object_label}: {note.text}</li>)}</ul> : <p>Замечаний пока нет. Это не означает, что оборудование уже проверено.</p>}
      {editable && <form onSubmit={event => void saveNote(event)}>
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
function EntryEditor({ entry, disabled, onSave }: { entry: Entry; disabled: boolean; onSave: (data: FormData) => Promise<void> }) {
  const [state, setState] = useState(entry.answered_by ? 'Сохранено' : 'Не заполнено');
  const [uncertain, setUncertain] = useState(false);
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const data = new FormData(event.currentTarget); setState('Сохраняется…');
    try { await onSave(data); setState('Сохранено'); }
    catch { setState('Ошибка. Перечитайте данные перед повтором.'); setUncertain(true); }
  }
  return <form className="checklist-item" onSubmit={event => void save(event)} onChange={() => setState('Не сохранено')}>
    <fieldset disabled={disabled || uncertain}>
      <legend>{entry.label}</legend>
      {entry.help_text && <p>{entry.help_text}</p>}
      <label>Ответ<select name="answer" defaultValue={entry.answer}><option value="unanswered">Не отвечено</option><option value="yes">Да</option><option value="no">Нет</option></select></label>
      {entry.equipment_check && <label><input className="inline" name="issue" type="checkbox" defaultChecked={entry.equipment_issue} /> Обнаружена неисправность — требуется связанная заметка</label>}
      <label>Комментарий<textarea name="comment" maxLength={4000} defaultValue={entry.comment} /></label>
      <button>Сохранить ответ</button><span role="status">{state}</span>
    </fieldset>
  </form>;
}
