import { useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, APIError } from './api';
import type { Identity } from './api';
import { formatRubles, parseRubles, rublesInput, stringifyExact } from './money';
import { ActionRequirements } from './BlockerList';
import type { Blocker } from './BlockerList';

type Movement = { id: string; kind: string; amount_kopecks: string; reversal_of: string | null; reason: string; recipient: string; occurred_at: string };
export type CashView = {
  session: { version: number; opening_cash_kopecks: string;counted_opening_kopecks:string|null;opening_confirmed_at:string|null; langame_cash_amount_kopecks: string | null; extra_cash_refunds_kopecks: string | null; extra_cash_expenses_kopecks: string | null; counted_closing_kopecks: string | null; movements: Movement[] };
  summary: { expected_closing_kopecks: string | null; difference_kopecks: string | null; collection_total_kopecks: string | null; policy_version: string; verified: boolean; blockers: { code: string; message: string }[] };
};
type Props = { identity: Identity; shift: { id: string; owner_id: string; status: string; version: number; cash?: CashView | null }; draftsPending: boolean; readyToActivate: boolean; activationBlockers: Blocker[]; onNavigate: (blocker: Blocker) => void; refresh: () => Promise<void> };
export function CashPanel({ identity, shift, readyToActivate, refresh, draftsPending, activationBlockers, onNavigate }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [uncertain, setUncertain] = useState(false);
  const [noRefunds, setNoRefunds] = useState(shift.cash?.session.extra_cash_refunds_kopecks === '0');
  const [noExpenses, setNoExpenses] = useState(shift.cash?.session.extra_cash_expenses_kopecks === '0');
  const attempt = useRef<{ route: string; body: string; key: string } | null>(null);
  const inFlight = useRef(false);
  const needsRead = useRef(false);
  if (!shift.cash) return <p>Кассовая запись не настроена.</p>;
  const { session, summary } = shift.cash;
  const editable = identity.pin_session && identity.device_trusted && identity.user.id === shift.owner_id;
  const actor = identity.user.id;
  const path = '/shifts/' + shift.id;
  const text = (data: FormData, key: string) => String(data.get(key) ?? '');
  const nullable = (raw: string) => raw.trim() === '' ? null : parseRubles(raw);
  const nonnegative = (raw: string) => { const value = parseRubles(raw); if (value < 0n) throw new Error('Сумма пересчёта или инкассации не может быть отрицательной'); return value; };
  async function proof(pin: string, action: string, target: string, revision: number) {
    return (await api<{ proof: string }>('/auth/reauth', 'POST', { pin, action, target_id: target, revision }, actor)).proof;
  }
  async function send<T>(route: string, method: string, body: unknown): Promise<T> {
    const canonical = stringifyExact(body);
    if (attempt.current?.route !== route || attempt.current.body !== canonical) attempt.current = { route, body: canonical, key: crypto.randomUUID() };
    return api<T>(route, method, body, actor, attempt.current.key);
  }
  async function submit(event: FormEvent<HTMLFormElement>, work: (data: FormData) => Promise<void>) {
    event.preventDefault();
    if (inFlight.current || needsRead.current || draftsPending) return;
    inFlight.current = true;
    const form = event.currentTarget; const data = new FormData(form);
    form.querySelectorAll<HTMLInputElement>('input[type="password"]').forEach(input => { input.value = ''; });
    setBusy(true); setError('');
    try { await work(data); form.reset(); await refresh(); }
    catch (cause) {
      setError(cause instanceof APIError ? cause.message : cause instanceof TypeError ? 'Сетевой результат неизвестен. Перечитайте данные перед новой операцией.' : cause instanceof Error ? cause.message : 'Действие не выполнено');
      // Never blindly recreate a monetary operation with a new proof/key after a timeout.
      setUncertain(true);
      needsRead.current = true;
    } finally { inFlight.current = false; setBusy(false); }
  }
  const disabled = busy || uncertain || draftsPending;
  const pin = <label>Личный PIN для этого действия<input name="pin" type="password" inputMode="numeric" pattern="[0-9]{6}" maxLength={6} required autoComplete="off" /></label>;
  return <div className="cash-panel" id="shift-cash" tabIndex={-1}>
    <h3>Тестовый расчёт кассы · {summary.policy_version}</h3>
    <p>Политика не проверена на реальной смене. Вводите только синтетические данные. LANGAME здесь означает только вручную внесённое изменение наличных текущей смены.</p>
    {error && <p role="alert" className="error">{error} <button type="button" onClick={() => void refresh().catch(() => {})}>Перечитать кассу</button></p>}
    <dl><dt>Начальный остаток</dt><dd>{formatRubles(session.opening_cash_kopecks)}</dd><dt>Инкассации минус отмены</dt><dd>{formatRubles(summary.collection_total_kopecks)}</dd><dt>Ожидаемый конец</dt><dd>{formatRubles(summary.expected_closing_kopecks)}</dd><dt>Разница пересчёта</dt><dd>{formatRubles(summary.difference_kopecks)}</dd></dl>
    {editable && shift.status === 'opening' && <form onSubmit={event => void submit(event, async data => {
      const counted = nonnegative(text(data, 'counted'));
      const token = await proof(text(data, 'pin'), 'activate_first_shift', shift.id, shift.version);
      await send(path + '/activate', 'POST', { counted_opening_kopecks: counted, reauth_proof: token, version: shift.version });
    })}>
      <h4>Лично принять первую смену · Test-mode</h4>
      <p>Требуется весь чек-лист приёма, собственный пересчёт и PIN. Начальная сумма от управляющего не считается принятой автоматически.</p>
      <label>Мой пересчёт в рублях<input name="counted" inputMode="decimal" required /></label>{pin}<button disabled={disabled || !readyToActivate} aria-describedby={activationBlockers.length ? 'activate-blockers' : undefined}>Подтвердить первый приём</button>
      <ActionRequirements id="activate-blockers" blockers={activationBlockers} onNavigate={onNavigate} />
    </form>}
    {editable && shift.status === 'active' && <>
      <form onSubmit={event => void submit(event, async data => {
        await send(path + '/cash', 'PATCH', { langame_cash_amount_kopecks: nullable(text(data, 'langame')), extra_cash_refunds_kopecks: noRefunds ? 0n : nullable(text(data, 'refunds')), extra_cash_expenses_kopecks: noExpenses ? 0n : nullable(text(data, 'expenses')), counted_closing_kopecks: nullable(text(data, 'counted')), version: session.version });
      })}>
        <label>Чистое изменение наличных по отчёту LANGAME · рубли<input name="langame" inputMode="decimal" defaultValue={rublesInput(session.langame_cash_amount_kopecks)} /></label>
        <label><input className="inline" type="checkbox" checked={noRefunds} onChange={event => setNoRefunds(event.target.checked)} /> Дополнительных возвратов, не учтённых в LANGAME, не было</label>
        <label>Возвраты, ещё не учтённые в LANGAME · рубли<input name="refunds" inputMode="decimal" disabled={noRefunds} defaultValue={rublesInput(session.extra_cash_refunds_kopecks)} /></label>
        <label><input className="inline" type="checkbox" checked={noExpenses} onChange={event => setNoExpenses(event.target.checked)} /> Дополнительных расходов, не учтённых в LANGAME, не было</label>
        <label>Расходы, ещё не учтённые в LANGAME · рубли<input name="expenses" inputMode="decimal" disabled={noExpenses} defaultValue={rublesInput(session.extra_cash_expenses_kopecks)} /></label>
        <label>Мой пересчёт после изъятий · рубли<input name="counted" inputMode="decimal" defaultValue={rublesInput(session.counted_closing_kopecks)} /></label>
        <button disabled={disabled}>Сохранить кассовый черновик</button>
      </form>
      <form onSubmit={event => void submit(event, async data => {
        const amount = nonnegative(text(data, 'amount'));
        const token = await proof(text(data, 'pin'), 'collection', shift.id, session.version);
        await send(path + '/cash/collections', 'POST', { amount_kopecks: amount, reason: text(data, 'reason'), recipient: text(data, 'recipient'), occurred_at: text(data, 'occurred'), reauth_proof: token, version: session.version });
      })}>
        <h4>Записать инкассацию</h4><label>Сумма · рубли<input name="amount" inputMode="decimal" required /></label>
        <label>Время изъятия с часовым смещением / UTC<input name="occurred" defaultValue={new Date().toISOString()} required /></label>
        <label>Основание<input name="reason" maxLength={4000} required /></label><label>Получатель / комментарий<input name="recipient" maxLength={200} /></label>{pin}<button disabled={disabled}>Записать с личным PIN</button>
      </form>
    </>}
    <h4>Журнал изъятий и отмен</h4>
    {session.movements.length === 0 ? <p>Записей пока нет.</p> : <ul>{session.movements.map(movement => <li key={movement.id}>
      {movement.kind === 'collection' ? 'Инкассация' : 'Отмена'}: {formatRubles(movement.amount_kopecks)} · {movement.reason} · {movement.occurred_at}
      {editable && shift.status === 'active' && movement.kind === 'collection' && !session.movements.some(other => other.reversal_of === movement.id) && <form onSubmit={event => void submit(event, async data => {
        const token = await proof(text(data, 'pin'), 'reverse_collection', movement.id, session.version);
        await send(path + '/cash/collections/' + movement.id + '/reverse', 'POST', { reason: text(data, 'reason'), reauth_proof: token, version: session.version });
      })}><label>Причина отмены<input name="reason" required maxLength={4000} /></label>{pin}<button disabled={disabled}>Отменить обратной записью</button></form>}
    </li>)}</ul>}
    {shift.status === 'active' && <ul>{summary.blockers.map(blocker => <li key={blocker.code}>{blocker.message}</li>)}</ul>}
    <p>PIN подтверждает запись сотрудника, но не доказывает физическое изъятие. Для передачи используйте отдельную форму и личный вход принимающего.</p>
  </div>;
}
