import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api } from './api';
import type { Identity, User } from './api';
import { actionFeedback } from './action-feedback';
import type { ActionFeedback } from './action-feedback';
import { formatRubles, parseRubles, stringifyExact } from './money';

type Shift = { id: string; owner_id: string; version: number; status: string; handover_id: string; handover_parent_id: string };
type Handover = {
  id: string; from_user_id: string; to_user_id: string; to_shift_id: string; revision: number; version: number; status: string;
  expected_cash_kopecks: string; counted_cash_kopecks: string | null; acknowledged_note_ids: string[];
  snapshot: { notes: { id: string; object_label: string; text: string }[]; checklist: { code: string; phase: string; label: string; answer: string; comment: string }[] };
};
type Props = {
  identity: Identity; shift: Shift; users: User[]; canPrepare: boolean; checklistReady: boolean;
  refresh: () => Promise<void>; onSessionEnd: () => Promise<Identity | null>;
};

export function HandoverPanel({ identity, shift, users, canPrepare, checklistReady, refresh, onSessionEnd }: Props) {
  const [handover, setHandover] = useState<Handover | null>(null);
  const [feedback, setFeedback] = useState<ActionFeedback | null>(null);
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
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

  useEffect(() => { if (feedback) alert.current?.focus(); }, [feedback]);

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
    if (inFlight.current || needsRead.current) return;
    inFlight.current = true; saved.current = false;
    const form = event.currentTarget;
    const data = new FormData(form);
    form.querySelectorAll<HTMLInputElement>('input[type="password"]').forEach(input => { input.value = ''; });
    setBusy(true); setFeedback(null);
    try { await work(data); }
    catch (cause) {
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
  const disabled = busy || uncertain;
  const receiving = handover?.status === 'pending' && actor === handover.to_user_id && own;
  const blockers: string[] = [];
  if (receiving && handover) {
    if (!checklistReady) blockers.push('Заполните и сохраните все 12 пунктов своего приёма.');
    if (handover.counted_cash_kopecks === null) blockers.push('Сохраните собственный пересчёт наличных.');
    else if (handover.counted_cash_kopecks !== handover.expected_cash_kopecks) blockers.push('Ваш пересчёт не совпадает с передаваемой суммой. Пересчитайте наличные; при ошибке сдачи попросите сдающего отозвать передачу.');
    if (handover.snapshot.notes.some(note => !handover.acknowledged_note_ids.includes(note.id))) blockers.push('Прочитайте замечания сдающего и сохраните ознакомление со всеми.');
  }

  return <section id="shift-handover" tabIndex={-1} aria-label="Передача смены" aria-busy={busy}>
    <h3>Передача смены · Тестовый режим</h3>
    <p>До окончательного приёма ответственным остаётся сдающий. Каждый сотрудник входит лично на этом же зарегистрированном браузере.</p>
    {feedback && <div role="alert" tabIndex={-1} ref={alert}><p>{feedback.message}</p><p>{feedback.instruction}</p></div>}
    {busy && <p role="status">Проверяем или отправляем данные. Дождитесь результата; повторно нажимать не нужно.</p>}
    <button type="button" disabled={busy} onClick={() => void reconcile()}>Проверить сессию и перечитать передачу</button>
    {own && shift.status === 'active' && <form onSubmit={event => void submit(event, async data => {
      const token = await proof(text(data, 'pin'), 'prepare_handover', shift.id, shift.version);
      await send('/shifts/' + shift.id + '/handovers', 'POST', { receiver_id: text(data, 'receiver'), version: shift.version, reauth_proof: token });
      await onSessionEnd();
    })}>
      <fieldset disabled={disabled}><legend>Подготовить сдачу</legend>
        <p>Заполните сдачу и сверьте кассу. После подготовки ваша сессия завершится; принимающий должен войти под своим именем. Новые внешние платежи требуют отзыва передачи и обновления отчёта.</p>
        {!canPrepare && <p>Подготовка пока недоступна. Откройте список «Что осталось проверить» выше: он показывает несохранённые ответы, замечания и кассовые причины.</p>}
        <label>Принимающий<select name="receiver" defaultValue="" required><option value="" disabled>Выберите сотрудника</option>{users.filter(user => user.active && user.roles.includes('admin') && user.id !== actor).map(user => <option key={user.id} value={user.id}>{user.display_name}</option>)}</select></label>
        {pin}<button disabled={!canPrepare}>Подготовить передачу и выйти</button>
      </fieldset>
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
        {blockers.length > 0 && <aside className="notice" aria-label="Что нужно для приёма"><strong>До личного приёма осталось</strong><ul>{blockers.map(reason => <li key={reason}>{reason}</li>)}</ul></aside>}
        <form onSubmit={event => void submit(event, async data => {
          const amount = parseRubles(text(data, 'counted'));
          if (amount < 0n) throw new Error('Пересчёт не может быть отрицательным');
          await send('/handovers/' + handover.id + '/receipt', 'PATCH', { revision: handover.revision, version: handover.version, counted_cash_kopecks: amount }); await refresh();
        })}><fieldset disabled={disabled}><legend>Пересчёт принимающего</legend><label>Мой пересчёт наличных · рубли<input name="counted" inputMode="decimal" required /></label><button>Сохранить мой пересчёт</button></fieldset></form>
        {handover.snapshot.notes.length > 0 && <form onSubmit={event => void submit(event, async data => {
          await send('/handovers/' + handover.id + '/acknowledgements', 'POST', { revision: handover.revision, version: handover.version, note_ids: data.getAll('notes').map(String) }); await refresh();
        })}><fieldset disabled={disabled}><legend>Ознакомление с замечаниями</legend>
          {handover.snapshot.notes.map(note => <label key={note.id}><input className="inline" type="checkbox" name="notes" value={note.id} defaultChecked={handover.acknowledged_note_ids.includes(note.id)} disabled={handover.acknowledged_note_ids.includes(note.id)} />{note.object_label}: {note.text} — ознакомился</label>)}
          <button>Сохранить ознакомление</button>
        </fieldset></form>}
        <form onSubmit={event => void submit(event, async data => {
          const token = await proof(text(data, 'pin'), 'accept_handover', handover.id, shift.version);
          await send('/handovers/' + handover.id + '/accept', 'POST', { revision: handover.revision, version: handover.version, shift_version: shift.version, reauth_proof: token }); await refresh();
        })}><fieldset disabled={disabled}><legend>Личное подтверждение приёма</legend>{pin}<button disabled={blockers.length > 0}>Лично принять смену</button></fieldset></form>
      </>}
    </>}
  </section>;
}
