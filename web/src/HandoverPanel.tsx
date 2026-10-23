import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api } from './api';
import type { Identity, User } from './api';
import { actionFeedback } from './action-feedback';
import type { ActionFeedback } from './action-feedback';
import { ActionRequirements } from './BlockerList';
import type { Blocker } from './BlockerList';
import { formatRubles, MoneyInputError, parseRubles, stringifyExact } from './money';

type Shift = { id: string; owner_id: string; version: number; status: string; handover_id: string; handover_parent_id: string };
type Handover = {
  id: string; from_user_id: string; to_user_id: string; to_shift_id: string; revision: number; version: number; status: string;
  expected_cash_kopecks: string; counted_cash_kopecks: string | null; acknowledged_note_ids: string[];
  snapshot: { notes: { id: string; object_label: string; text: string }[]; checklist: { code: string; phase: string; label: string; answer: string; comment: string }[] };
};
type Props = {
  identity: Identity; shift: Shift; users: User[]; canPrepare: boolean; checklistReady: boolean;
  draftsPending: boolean; readinessKnown: boolean; readinessBlockers: Blocker[]; acceptanceBlockers: Blocker[]; onNavigate: (blocker: Blocker) => void;
  refresh: () => Promise<void>; onSessionEnd: () => Promise<Identity | null>;
};

export function HandoverPanel({ identity, shift, users, canPrepare, checklistReady, refresh, onSessionEnd, draftsPending, readinessKnown, readinessBlockers, acceptanceBlockers, onNavigate }: Props) {
  const [handover, setHandover] = useState<Handover | null>(null);
  const [feedback, setFeedback] = useState<ActionFeedback | null>(null);
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [inputInvalid, setInputInvalid] = useState(false);
  const receipt = useRef<HTMLInputElement>(null);
  // Refs also guard two submits in one browser event, before React renders disabled.
  const inFlight = useRef(false);
  const needsRead = useRef(false);
  const saved = useRef(false);
  const alert = useRef<HTMLDivElement>(null);
  const attempt = useRef<{ route: string; body: string; key: string } | null>(null);
  const actor = identity.user.id;
  const own = identity.pin_session && identity.device_trusted && shift.owner_id === actor;

  useEffect(() => {
    if (!shift.handover_id) return;
    let live = true;
    void api<Handover>('/handovers/' + shift.handover_id).then(result => { if (live) setHandover(result); }).catch(() => {
      if (!live) return;
      needsRead.current = true; setUncertain(true);
      setFeedback({ message: 'Снимок передачи не получен.', instruction: 'Проверьте сессию и перечитайте передачу. Подтверждать её без актуальных данных нельзя.' });
    });
    return () => { live = false; };
  }, [shift.handover_id]);

  useEffect(() => {
    if (inputInvalid) receipt.current?.focus();
    else if (feedback) alert.current?.focus();
  }, [feedback, inputInvalid]);

  async function send(route: string, method: string, body: unknown) {
    const canonical = stringifyExact(body);
    if (attempt.current?.route !== route || attempt.current.body !== canonical) attempt.current = { route, body: canonical, key: crypto.randomUUID() };
    const result = await api<Handover>(route, method, body, actor, attempt.current.key);
    saved.current = true;
    return result;
  }
  async function proof(pin: string, action: string, target: string, version: number) {
    return (await api<{ proof: string }>('/auth/reauth', 'POST', { pin, action, target_id: target, revision: version }, actor)).proof;
  }
  async function submit(event: FormEvent<HTMLFormElement>, work: (data: FormData) => Promise<void>) {
    event.preventDefault();
    if (inFlight.current || needsRead.current || draftsPending) return;
    inFlight.current = true; saved.current = false;
    const form = event.currentTarget;
    const data = new FormData(form);
    form.querySelectorAll<HTMLInputElement>('input[type="password"]').forEach(input => { input.value = ''; });
    setBusy(true); setFeedback(null); setInputInvalid(false);
    try { await work(data); }
    catch (cause) {
      if (cause instanceof MoneyInputError && !saved.current) {
        setInputInvalid(true);
        setFeedback({ message: cause.message, instruction: 'Исправьте сумму и сохраните пересчёт. Данные на сервер не отправлялись.' });
        return;
      }
      needsRead.current = true; setUncertain(true);
      setFeedback(actionFeedback(cause, saved.current));
    } finally { inFlight.current = false; setBusy(false); }
  }
  async function reconcile() {
    if (inFlight.current) return;
    inFlight.current = true; setBusy(true);
    try {
      const current = await onSessionEnd();
      // Do not refresh an old employee's form after logout or a user switch.
      if (current?.user.id === actor) await refresh();
    } catch {
      needsRead.current = true; setUncertain(true);
      setFeedback({ message: 'Не удалось проверить сессию и состояние передачи.', instruction: 'Дождитесь связи и повторите проверку. Повторная отправка действия остаётся недоступной.' });
    } finally { inFlight.current = false; setBusy(false); }
  }

  const text = (data: FormData, key: string) => String(data.get(key) ?? '');
  const pin = <label>Мой личный PIN<input name="pin" type="password" inputMode="numeric" pattern="[0-9]{6}" maxLength={6} required autoComplete="off" /></label>;
  const disabled = busy || uncertain || draftsPending;
  const receiving = handover?.status === 'pending' && actor === handover.to_user_id && own;
  const blockers: Blocker[] = [];
  if (receiving && handover) {
    if (!readinessKnown) blockers.push({ code: 'READINESS_UNKNOWN', message: 'Готовность смены не получена. Перечитайте сохранённые данные.' });
    else if (!checklistReady) blockers.push(...(acceptanceBlockers.length ? acceptanceBlockers : [{ code: 'CHECKLIST_INCOMPLETE', message: 'Заполните и сохраните все пункты своего приёма.' }]));
    blockers.push(...readinessBlockers.filter(blocker => ['UNSAVED_ANSWER', 'UNSAVED_NOTE', 'STORAGE_UNKNOWN', 'STORAGE_SAVING'].includes(blocker.code)));
    if (handover.counted_cash_kopecks === null) blockers.push({ code: 'RECEIPT_REQUIRED', message: 'Сохраните собственный пересчёт наличных.' });
    else if (handover.counted_cash_kopecks !== handover.expected_cash_kopecks) blockers.push({ code: 'RECEIPT_MISMATCH', message: 'Ваш пересчёт не совпадает с передаваемой суммой. Пересчитайте наличные; при ошибке сдачи попросите сдающего отозвать передачу.' });
    if (handover.snapshot.notes.some(note => !handover.acknowledged_note_ids.includes(note.id))) blockers.push({ code: 'NOTE_ACK_REQUIRED', message: 'Прочитайте замечания сдающего и сохраните ознакомление со всеми.' });
    if (uncertain) blockers.unshift({ code: 'ACTION_UNKNOWN', message: 'Проверьте сессию и перечитайте передачу перед новым подтверждением.' });
  }
  const prepareBlockers = readinessBlockers.length ? [...readinessBlockers] : !canPrepare ? [{ code: 'READINESS_UNKNOWN', message: 'Подготовка пока не разрешена. Перечитайте сохранённые данные.' }] : [];
  if (uncertain) prepareBlockers.unshift({ code: 'ACTION_UNKNOWN', message: 'Проверьте сессию и перечитайте передачу перед новым подтверждением.' });

  return <section id="shift-handover" tabIndex={-1} aria-label="Передача смены" aria-busy={busy}>
    <h3>Передача смены · Тестовый режим</h3>
    <p>До окончательного приёма ответственным остаётся сдающий. Каждый сотрудник входит лично на этом же зарегистрированном браузере.</p>
    {feedback && <div id="handover-feedback" role="alert" tabIndex={-1} ref={alert}><p>{feedback.message}</p><p>{feedback.instruction}</p></div>}
    {busy && <p role="status">Проверяем или отправляем данные. Дождитесь результата; повторно нажимать не нужно.</p>}
    <button id="handover-reconcile" type="button" disabled={busy} onClick={() => void reconcile()}>Проверить сессию и перечитать передачу</button>
    {own && shift.status === 'active' && <form onSubmit={event => void submit(event, async data => {
      const token = await proof(text(data, 'pin'), 'prepare_handover', shift.id, shift.version);
      await send('/shifts/' + shift.id + '/handovers', 'POST', { receiver_id: text(data, 'receiver'), version: shift.version, reauth_proof: token });
      await onSessionEnd();
    })}>
      <fieldset disabled={disabled}><legend>Подготовить сдачу</legend>
        <p>Заполните сдачу и сверьте кассу. После подготовки ваша сессия завершится; принимающий должен войти под своим именем. Новые внешние платежи требуют отзыва передачи и обновления отчёта.</p>
        <label>Принимающий<select name="receiver" defaultValue="" required><option value="" disabled>Выберите сотрудника</option>{users.filter(user => user.active && user.roles.includes('admin') && user.id !== actor).map(user => <option key={user.id} value={user.id}>{user.display_name}</option>)}</select></label>
        {pin}<button disabled={!canPrepare || !readinessKnown} aria-describedby={prepareBlockers.length ? 'prepare-blockers' : undefined}>Подготовить передачу и выйти</button>
      </fieldset>
      <ActionRequirements id="prepare-blockers" blockers={prepareBlockers} onNavigate={onNavigate} />
    </form>}
    {shift.handover_id && !handover && <p>{feedback ? 'Без снимка доступны только повторная проверка и личный вход.' : 'Загружаем снимок передачи…'}</p>}
    {handover && <>
      <p>Редакция {handover.revision} · {handover.status === 'pending' ? 'Ожидает приёма' : handover.status === 'accepted' ? 'Принята' : 'Отозвана'}</p>
      <p>Передаваемая сумма: {formatRubles(handover.expected_cash_kopecks)}. Сохранённый пересчёт получателя: {formatRubles(handover.counted_cash_kopecks)}.</p>
      <details><summary>Ответы сдающего</summary><ul>{handover.snapshot.checklist.filter(entry => entry.phase === 'closing').map(entry => <li key={entry.code}>{entry.label}: {entry.answer === 'yes' ? 'Да' : entry.answer === 'no' ? 'Нет' : 'Не заполнено'} {entry.comment}</li>)}</ul></details>
      {handover.snapshot.notes.length === 0 && <p>У сдающего нет записанных замечаний.</p>}
      {handover.status === 'pending' && actor === handover.from_user_id && identity.pin_session && <form onSubmit={event => void submit(event, async data => {
        await send('/handovers/' + handover.id + '/cancel', 'POST', { version: handover.version, reason: text(data, 'reason') }); await refresh();
      })}><fieldset disabled={disabled}><legend>Отзыв передачи</legend><label>Причина отзыва<textarea name="reason" required maxLength={4000} /></label><button>Отозвать и вернуться к своей смене</button></fieldset></form>}
      {receiving && <>
        <p>Сумма станет вашим начальным остатком только после окончательного подтверждения.</p>
        <form onSubmit={event => void submit(event, async data => {
          const amount = parseRubles(text(data, 'counted'));
          if (amount < 0n) throw new MoneyInputError('Пересчёт не может быть отрицательным');
          await send('/handovers/' + handover.id + '/receipt', 'PATCH', { revision: handover.revision, version: handover.version, counted_cash_kopecks: amount }); await refresh();
        })}><fieldset id="handover-receipt" tabIndex={-1} disabled={disabled}><legend>Пересчёт принимающего</legend><label>Мой пересчёт наличных · рубли<input name="counted" inputMode="decimal" required ref={receipt} aria-invalid={inputInvalid || undefined} aria-describedby={inputInvalid ? 'handover-feedback' : undefined} onChange={() => { if (inputInvalid) { setInputInvalid(false); setFeedback(null); } }} /></label><button>Сохранить мой пересчёт</button></fieldset></form>
        {handover.snapshot.notes.length > 0 && <form onSubmit={event => void submit(event, async data => {
          await send('/handovers/' + handover.id + '/acknowledgements', 'POST', { revision: handover.revision, version: handover.version, note_ids: data.getAll('notes').map(String) }); await refresh();
        })}><fieldset id="handover-notes" tabIndex={-1} disabled={disabled}><legend>Ознакомление с замечаниями</legend>
          {handover.snapshot.notes.map(note => <label key={note.id}><input className="inline" type="checkbox" name="notes" value={note.id} defaultChecked={handover.acknowledged_note_ids.includes(note.id)} disabled={handover.acknowledged_note_ids.includes(note.id)} />{note.object_label}: {note.text} — ознакомился</label>)}
          <button>Сохранить ознакомление</button>
        </fieldset></form>}
        <form onSubmit={event => void submit(event, async data => {
          const token = await proof(text(data, 'pin'), 'accept_handover', handover.id, shift.version);
          await send('/handovers/' + handover.id + '/accept', 'POST', { revision: handover.revision, version: handover.version, shift_version: shift.version, reauth_proof: token }); await refresh();
        })}><fieldset disabled={disabled}><legend>Личное подтверждение приёма</legend>{pin}<button disabled={blockers.length > 0} aria-describedby={blockers.length ? 'accept-blockers' : undefined}>Лично принять смену</button></fieldset>
          <ActionRequirements id="accept-blockers" blockers={blockers} onNavigate={onNavigate} />
        </form>
      </>}
    </>}
  </section>;
}
