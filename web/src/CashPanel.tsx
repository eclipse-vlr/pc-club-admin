import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, APIError } from './api';
import type { Identity } from './api';
import { formatRubles, MoneyInputError, parseRubles, rublesInput, stringifyExact } from './money';
import { ActionRequirements } from './BlockerList';
import type { Blocker } from './BlockerList';
import { confirmCashDiscard } from './checklist-drafts';

type Movement = { id: string; kind: string; amount_kopecks: string; reversal_of: string | null; reason: string; recipient: string; occurred_at: string };
export type CashView = {
  session: { version: number; opening_cash_kopecks: string;counted_opening_kopecks:string|null;opening_confirmed_at:string|null; langame_cash_amount_kopecks: string | null; extra_cash_refunds_kopecks: string | null; extra_cash_expenses_kopecks: string | null; counted_closing_kopecks: string | null; movements: Movement[] };
  summary: { expected_closing_kopecks: string | null; difference_kopecks: string | null; collection_total_kopecks: string | null; policy_version: string; verified: boolean; blockers: { code: string; message: string }[] };
};
type Props = { identity: Identity; shift: { id: string; owner_id: string; status: string; version: number; cash?: CashView | null }; draftsPending: boolean; readyToActivate: boolean; activationBlockers: Blocker[]; onNavigate: (blocker: Blocker) => void; onDirty: (dirty: boolean) => void; refresh: () => Promise<void> };
export function CashPanel({ identity, shift, readyToActivate, refresh, draftsPending, activationBlockers, onNavigate, onDirty }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [uncertain, setUncertain] = useState(false);
  const [inputField, setInputField] = useState('');
  const panel = useRef<HTMLDivElement>(null);
  const confirmed = useRef(false);
  const [hasDraft, setHasDraft] = useState(false);
  const [draftCash, setDraftCash] = useState(shift.cash);
  const cash = hasDraft ? draftCash : shift.cash;
  const cashVersion = cash?.session.version;
  const staleCash = hasDraft && cashVersion !== shift.cash?.session.version;
  const baselines = useRef(new Map<HTMLFormElement, string>());
  const [collectionTime] = useState(() => new Date().toISOString());
  const [noRefunds, setNoRefunds] = useState(shift.cash?.session.extra_cash_refunds_kopecks === '0');
  const [noExpenses, setNoExpenses] = useState(shift.cash?.session.extra_cash_expenses_kopecks === '0');
  const attempt = useRef<{ route: string; body: string; key: string } | null>(null);
  const inFlight = useRef(false);
  const needsRead = useRef(false);
  useEffect(() => {
    if (inputField && !busy) panel.current?.querySelector<HTMLInputElement>('input[name="' + inputField + '"]')?.focus();
  }, [inputField, busy, error]);
  useEffect(() => {
    if (!hasDraft) {
      setDraftCash(shift.cash);
      setNoRefunds(shift.cash?.session.extra_cash_refunds_kopecks === '0');
      setNoExpenses(shift.cash?.session.extra_cash_expenses_kopecks === '0');
    }
  }, [hasDraft, shift.cash]);
  useEffect(() => {
    if (hasDraft) return;
    baselines.current.clear();
    for (const form of panel.current?.querySelectorAll('form') ?? []) baselines.current.set(form, cashFormSignature(form));
  }, [cashVersion, hasDraft, noRefunds, noExpenses]);
  useEffect(() => () => { onDirty(false); }, [onDirty]);
  function notifyDrafts() {
    const dirty = !!panel.current?.querySelector('form[data-cash-dirty="true"]');
    setHasDraft(dirty); onDirty(dirty);
  }
  function trackDraft(form: HTMLFormElement) {
    if (cashFormSignature(form) !== baselines.current.get(form)) form.dataset.cashDirty = 'true';
    else delete form.dataset.cashDirty;
    notifyDrafts();
  }
  if (!cash) return <p>Кассовая запись не настроена.</p>;
  const { session, summary } = cash;
  const editable = identity.pin_session && identity.device_trusted && identity.user.id === shift.owner_id;
  const actor = identity.user.id;
  const path = '/shifts/' + shift.id;
  const text = (data: FormData, key: string) => String(data.get(key) ?? '');
  function amount(data: FormData, field: string, nonnegative = false) {
    try {
      const value = parseRubles(text(data, field));
      if (nonnegative && value < 0n) throw new MoneyInputError('Сумма не может быть отрицательной.');
      return value;
    } catch (cause) {
      if (cause instanceof MoneyInputError) throw new MoneyInputError(cause.message, field);
      throw cause;
    }
  }
  const nullable = (data: FormData, field: string, nonnegative = true) => text(data, field).trim() === '' ? null : amount(data, field, nonnegative);
  const invalid = (field: string) => ({ 'aria-invalid': inputField === field || undefined, 'aria-describedby': inputField === field ? 'cash-error' : 'cash-amount-help' });
  async function proof(pin: string, action: string, target: string, revision: number) {
    return (await api<{ proof: string }>('/auth/reauth', 'POST', { pin, action, target_id: target, revision }, actor)).proof;
  }
  async function send<T>(route: string, method: string, body: unknown): Promise<T> {
    const canonical = stringifyExact(body);
    if (attempt.current?.route !== route || attempt.current.body !== canonical) attempt.current = { route, body: canonical, key: crypto.randomUUID() };
    const result = await api<T>(route, method, body, actor, attempt.current.key);
    confirmed.current = true;
    return result;
  }
  async function submit(event: FormEvent<HTMLFormElement>, work: (data: FormData) => Promise<void>) {
    event.preventDefault();
    if (inFlight.current || needsRead.current || draftsPending || staleCash) return;
    if (!confirmCashDiscard(event.currentTarget)) return;
    inFlight.current = true; confirmed.current = false;
    const form = event.currentTarget; const data = new FormData(form);
    form.querySelectorAll<HTMLInputElement>('input[type="password"]').forEach(input => { input.value = ''; });
    setBusy(true); setError(''); setInputField('');
    try { await work(data); form.reset(); delete form.dataset.cashDirty; notifyDrafts(); await refresh(); }
    catch (cause) {
      if (cause instanceof MoneyInputError && !confirmed.current) {
        setError(cause.message); setInputField(cause.field);
        return;
      }
      setError(confirmed.current ? 'Действие сохранено, но обновлённые данные не получены. Перечитайте кассу; повторно отправлять действие не нужно.' : cause instanceof APIError ? cause.message : 'Результат действия неизвестен. Перечитайте кассу перед новой операцией.');
      // Never blindly recreate a monetary operation with a new proof/key after a timeout.
      setUncertain(true);
      needsRead.current = true;
    } finally { inFlight.current = false; setBusy(false); }
  }
  async function reconcile() {
    if (inFlight.current) return;
    inFlight.current = true; setBusy(true);
    try { await refresh(); }
    catch { setError('Не удалось перечитать кассу. Дождитесь связи и повторите проверку; новая запись пока недоступна.'); }
    finally { inFlight.current = false; setBusy(false); }
  }
  const disabled = busy || uncertain || draftsPending || staleCash;
  const pin = <label>Личный PIN для этого действия<input name="pin" type="password" inputMode="numeric" pattern="[0-9]{6}" maxLength={6} required autoComplete="off" /></label>;
  return <div className="cash-panel" id="shift-cash" tabIndex={-1} ref={panel} aria-busy={busy} onInput={event => {
    if (event.target instanceof HTMLInputElement && event.target.name === inputField) { setInputField(''); setError(''); }
  }} onChange={event => {
    const input = event.target;
    if (input instanceof HTMLInputElement && input.type === 'password') return;
    if ((input instanceof HTMLInputElement || input instanceof HTMLSelectElement || input instanceof HTMLTextAreaElement) && input.form) trackDraft(input.form);
  }}>
    <h3>Касса · тестовый режим</h3>
    <p>Политика не проверена на реальной смене. Вводите только синтетические данные. LANGAME здесь означает только вручную внесённое изменение наличных текущей смены.</p>
    {error && <div id="cash-error" role="alert" className="error"><p>{error}</p>{uncertain && <button type="button" disabled={busy} onClick={() => void reconcile()}>Перечитать кассу</button>}</div>}
    {busy && <p role="status">Проверяем или сохраняем данные кассы. Дождитесь результата.</p>}
    {hasDraft && <p className="unsaved-notice" role="status">Есть несохранённые данные кассы. Сохраните нужную форму перед передачей смены.</p>}
    {staleCash && <p className="error" role="alert">Касса изменилась в другом окне. Ваш ввод сохранён на этой странице. Перечитайте сохранённые данные смены перед новой записью.</p>}
    <p>Ниже показаны сохранённые суммы. Изменения в полях учитываются после сохранения.</p>
    <dl className="cash-summary"><div><dt>Начальный остаток</dt><dd>{formatRubles(session.opening_cash_kopecks)}</dd></div><div><dt>Инкассации минус отмены</dt><dd>{formatRubles(summary.collection_total_kopecks)}</dd></div><div><dt>Ожидаемый конец</dt><dd>{formatRubles(summary.expected_closing_kopecks)}</dd></div><div><dt>Разница пересчёта</dt><dd>{formatRubles(summary.difference_kopecks)}</dd></div></dl>
    <p id="cash-amount-help">Все суммы — в рублях, до двух знаков после запятой. Например: 1250,50. Пустое поле не означает ноль.</p>
    {editable && shift.status === 'opening' && <form key={'opening:' + cashVersion} onSubmit={event => void submit(event, async data => {
      const counted = amount(data, 'counted', true);
      const token = await proof(text(data, 'pin'), 'activate_first_shift', shift.id, shift.version);
      await send(path + '/activate', 'POST', { counted_opening_kopecks: counted, reauth_proof: token, version: shift.version });
    })}>
      <fieldset disabled={disabled}><legend>Лично принять первую смену · Тестовый режим</legend>
      <p>Требуется весь чек-лист приёма, собственный пересчёт и PIN. Начальная сумма от управляющего не считается принятой автоматически.</p>
      <label>Мой пересчёт в рублях<input name="counted" inputMode="decimal" required {...invalid('counted')} /></label>{pin}<button disabled={disabled || !readyToActivate} aria-describedby={activationBlockers.length ? 'activate-blockers' : undefined}>Подтвердить первый приём</button></fieldset>
      <ActionRequirements id="activate-blockers" blockers={activationBlockers} onNavigate={onNavigate} />
    </form>}
    {editable && shift.status === 'active' && <div className="cash-workflows">
      <form key={'cash:' + cashVersion} onSubmit={event => void submit(event, async data => {
        await send(path + '/cash', 'PATCH', { langame_cash_amount_kopecks: nullable(data, 'langame', false), extra_cash_refunds_kopecks: noRefunds ? 0n : nullable(data, 'refunds'), extra_cash_expenses_kopecks: noExpenses ? 0n : nullable(data, 'expenses'), counted_closing_kopecks: nullable(data, 'counted'), version: session.version });
      })}>
        <fieldset disabled={disabled}><legend>Сверка наличных</legend>
        <label>Чистое изменение наличных по отчёту LANGAME · рубли<input name="langame" inputMode="decimal" defaultValue={rublesInput(session.langame_cash_amount_kopecks)} {...invalid('langame')} /></label>
        <label><input className="inline" type="checkbox" checked={noRefunds} onChange={event => setNoRefunds(event.target.checked)} /> Дополнительных возвратов, не учтённых в LANGAME, не было</label>
        <label>Возвраты, ещё не учтённые в LANGAME · рубли<input name="refunds" inputMode="decimal" disabled={noRefunds} defaultValue={rublesInput(session.extra_cash_refunds_kopecks)} {...invalid('refunds')} /></label>
        <label><input className="inline" type="checkbox" checked={noExpenses} onChange={event => setNoExpenses(event.target.checked)} /> Дополнительных расходов, не учтённых в LANGAME, не было</label>
        <label>Расходы, ещё не учтённые в LANGAME · рубли<input name="expenses" inputMode="decimal" disabled={noExpenses} defaultValue={rublesInput(session.extra_cash_expenses_kopecks)} {...invalid('expenses')} /></label>
        <label>Мой пересчёт после изъятий · рубли<input name="counted" inputMode="decimal" defaultValue={rublesInput(session.counted_closing_kopecks)} {...invalid('counted')} /></label>
        <button disabled={disabled}>Сохранить кассовый черновик</button></fieldset>
      </form>
      <form key={'collection:' + cashVersion} onSubmit={event => void submit(event, async data => {
        const collected = amount(data, 'amount', true);
        const token = await proof(text(data, 'pin'), 'collection', shift.id, session.version);
        await send(path + '/cash/collections', 'POST', { amount_kopecks: collected, reason: text(data, 'reason'), recipient: text(data, 'recipient'), occurred_at: text(data, 'occurred'), reauth_proof: token, version: session.version });
      })}>
        <fieldset disabled={disabled}><legend>Записать инкассацию</legend><label>Сумма · рубли<input name="amount" inputMode="decimal" required {...invalid('amount')} /></label>
        <label>Время изъятия с часовым смещением / UTC<input name="occurred" defaultValue={collectionTime} required /></label>
        <label>Основание<input name="reason" maxLength={4000} required /></label><label>Получатель / комментарий<input name="recipient" maxLength={200} /></label>{pin}<button disabled={disabled}>Записать с личным PIN</button></fieldset>
      </form>
    </div>}
    <h4>Журнал изъятий и отмен</h4>
    {session.movements.length === 0 ? <p>Записей пока нет.</p> : <ul>{session.movements.map(movement => <li key={movement.id}>
      {movement.kind === 'collection' ? 'Инкассация' : 'Отмена'}: {formatRubles(movement.amount_kopecks)} · {movement.reason} · {movement.occurred_at}
      {editable && shift.status === 'active' && movement.kind === 'collection' && !session.movements.some(other => other.reversal_of === movement.id) && <form key={movement.id + ':' + cashVersion} onSubmit={event => void submit(event, async data => {
        const token = await proof(text(data, 'pin'), 'reverse_collection', movement.id, session.version);
        await send(path + '/cash/collections/' + movement.id + '/reverse', 'POST', { reason: text(data, 'reason'), reauth_proof: token, version: session.version });
      })}><fieldset disabled={disabled}><legend>Отмена инкассации</legend><label>Причина отмены<input name="reason" required maxLength={4000} /></label>{pin}<button disabled={disabled}>Отменить обратной записью</button></fieldset></form>}
    </li>)}</ul>}
    {shift.status === 'active' && <ul>{summary.blockers.map(blocker => <li key={blocker.code}>{blocker.message}</li>)}</ul>}
    <p>PIN подтверждает запись сотрудника, но не доказывает физическое изъятие. Для передачи используйте отдельную форму и личный вход принимающего.</p>
  </div>;
}

// Compare only non-secret controls in memory; PINs never enter draft snapshots.
function cashFormSignature(form: HTMLFormElement): string {
  return JSON.stringify(Array.from(form.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>('input,select,textarea'))
    .filter(input => !(input instanceof HTMLInputElement && input.type === 'password'))
    .map(input => [input.name, input instanceof HTMLInputElement && input.type === 'checkbox' ? input.checked : input.value]));
}
