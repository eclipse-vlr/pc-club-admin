import { useEffect, useRef, useState } from 'react';
import { api, APIError } from './api';
import { formatRubles } from './money';
import { InterfaceIcon } from './InterfaceIcon';
import { ProgressMeter } from './ProgressMeter';

type Progress = { completed: number; total: number; blockers: { code: string; message: string }[] };
type Cash = {
  opening_cash_kopecks: string; counted_opening_kopecks: string | null; opening_confirmed_at: string | null;
  counted_closing_kopecks: string | null; state: string; last_saved_at: string | null; changed_after_save: boolean;
  summary: { expected_closing_kopecks: string | null; difference_kopecks: string | null; collection_total_kopecks: string | null; policy_version: string; verified: boolean; blockers: { code: string; message: string }[] };
};
type Shift = {
  id: string; owner_name: string; business_date: string; kind: string; status: string;
  planned_start_at: string; planned_end_at: string; actual_start_at: string | null;
  overdue: boolean; delay_seconds: number; acceptance: Progress; closing: Progress; cash: Cash | null;
};
type Dashboard = {
  observed_at: string; timezone: string; state: string; cash_policy_status: string; production_blocked: boolean;
  responsible: Shift | null; initial_opening: Shift | null;
  pending_handover: { receipt_state: string; id: string; receiver_name: string; revision: number; prepared_at: string; expected_cash_kopecks: string; counted_cash_kopecks: string | null; acknowledged_notes: number; total_notes: number; acceptance: Progress } | null;
};
const cashStates: Record<string, string> = {
  opening_unconfirmed: 'Начальный остаток ещё не принят лично', incomplete: 'Не все кассовые поля сохранены',
  mismatch: 'Есть кассовое расхождение', matched: 'Сохранённые суммы совпадают', invalid: 'Кассовые данные требуют проверки',
};

export function ManagerDashboard() {
  const [data, setData] = useState<Dashboard | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState('');
  const reload = useRef<(() => Promise<void>) | null>(null);
  useEffect(() => {
    let live = true, inFlight = false;
    const load = async () => {
      if (inFlight || !live) return;
      inFlight = true; setBusy(true);
      try { const result = await api<Dashboard>('/manager/dashboard'); if (live) { setData(result); setError(''); } }
      catch (cause) { if (live) setError(cause instanceof APIError ? cause.message : 'Не удалось связаться с сервером'); }
      finally { inFlight = false; if (live) setBusy(false); }
    };
    reload.current = load; void load();
    // Passive reads do not keep a personal session alive.
    const timer = setInterval(() => { if (!document.hidden) void load(); }, 30_000);
    return () => { live = false; clearInterval(timer); if (reload.current === load) reload.current = null; };
  }, []);
  const time = (value: string | null) => value && data ? new Date(value).toLocaleString('ru-RU', { timeZone: data.timezone }) : 'Не зафиксировано';
  const clock = (value: string) => new Date(value).toLocaleTimeString('ru-RU', { timeZone: data?.timezone, hour: '2-digit', minute: '2-digit' });
  const shift = data?.responsible;
  const opening = data?.initial_opening;
  const current = shift ?? opening;
  const pending = data?.pending_handover;
  const blockers = current ? current.status === 'opening' ? current.acceptance.blockers : [...current.acceptance.blockers, ...current.closing.blockers] : [];
  const amount = pending ? pending.expected_cash_kopecks : shift ? shift.cash?.summary.expected_closing_kopecks : opening?.cash?.opening_cash_kopecks;
  return <section className="manager-dashboard" aria-label="Панель руководителя" aria-busy={busy} data-stale={error ? true : undefined}>
    <div className="panel-heading"><div><p className="eyebrow">Состояние клуба</p><h2>Панель руководителя</h2></div><div className="panel-tools">{data && <span className="updated-at">Снимок · {clock(data.observed_at)}</span>}<button disabled={busy} onClick={() => void reload.current?.()}>Обновить панель</button></div></div>
    {busy && <p className="snapshot-meta" role="status">Получаем состояние клуба…</p>}
    {error && <p role="alert">Ошибка обновления: {error}. {data ? 'Ниже сохранённый снимок; он может быть неактуален.' : 'Состояние клуба неизвестно.'}</p>}
    {data && <>
      {data.production_blocked && <p role="alert">Рабочие операции production заблокированы до проверки кассовой политики.</p>}
      {data.state === 'no_responsible' && <p role="alert">В истории есть смены, но подтверждённого ответственного сейчас нет. Требуется проверка.</p>}
      {current ? <>
        <div className="responsibility-board">
          <div className="responsibility-person"><span className="owner-avatar" aria-hidden="true">{current.owner_name.trim().charAt(0)}</span><div><p className="metric-label">{shift ? 'Сейчас отвечает за смену' : 'Первый приём · ожидает подтверждения'}</p><h3>{shift ? <><span className="visually-hidden">Ответственный: </span>{shift.owner_name}</> : <>Первый черновик · {current.owner_name}</>}</h3><p className="owner-shift">{current.business_date} <span aria-hidden="true">/</span> {current.kind === 'night' ? 'Ночь 21–09' : 'День 09–21'}</p></div><span className={'board-state board-state--' + (shift ? pending ? 'pending' : 'active' : 'opening')}>{shift ? pending ? 'Передача начата' : 'Активная смена' : 'Черновик'}</span></div>
          <div className="responsibility-amount"><span className="metric-label">{pending ? 'К передаче · тестовая сумма' : shift ? 'Ожидаемый остаток · тестовая сумма' : 'Исходный остаток · тестовая сумма'}</span><strong>{amount == null ? 'Не введён' : formatRubles(amount)}</strong><p>{pending ? 'До личного приёма отвечает сдающий' : shift ? 'По сохранённому кассовому отчёту' : 'Ответственный ещё не назначен'}</p></div>
          <div className="board-progress"><ProgressCard label="Приём" value={current.acceptance} /><ProgressCard label="Сдача" value={current.closing} /></div>
        </div>
        {shift?.overdue && <p className="overdue-notice" role="alert">Передача задержана на {Math.floor(shift.delay_seconds / 60)} мин. Ответственным остаётся {shift.owner_name}.</p>}
        {!shift && <p className="opening-notice"><InterfaceIcon name="shield" />Ответственный ещё не назначен: первый черновик должен быть лично принят.</p>}
        {blockers.length > 0 && <details className="dashboard-blockers"><summary>Требуют внимания <span>{blockers.length}</span></summary><ul>{blockers.map((blocker, index) => <li key={blocker.code + ':' + index}>{blocker.message}</li>)}</ul></details>}
        <div className="dashboard-workbench">
          <CashCard cash={current.cash} time={time} />
          {pending ? <section className="pending-card" aria-label="Ожидающая передача">
            <div className="workbench-heading"><span className="section-symbol"><InterfaceIcon name="arrow" /></span><div><p className="eyebrow">Следующий шаг</p><h3>Передача ожидает приёма</h3></div></div>
            <div className="transfer-people"><div><span>Сдаёт · ещё отвечает</span><strong>{shift?.owner_name ?? 'Не назначен'}</strong></div><InterfaceIcon name="arrow" /><div><span>Принимающий: </span><strong>{pending.receiver_name}.</strong></div></div>
            <p className={'receipt-result receipt-result--' + pending.receipt_state}>{pending.receipt_state === 'missing' ? 'Пересчёт получателя ещё не сохранён' : pending.receipt_state === 'mismatch' ? 'Пересчёт получателя не совпадает; приём заблокирован.' : pending.receipt_state === 'matched' ? 'Сохранённый пересчёт получателя совпадает с передаваемой суммой' : 'Состояние пересчёта получателя неизвестно'}</p>
            <dl className="transfer-facts"><div><dt>Чек-лист получателя</dt><dd>{pending.acceptance.completed} / {pending.acceptance.total}</dd></div><div><dt>Замечания прочитаны</dt><dd>{pending.acknowledged_notes} / {pending.total_notes}</dd></div><div><dt>Сумма к передаче</dt><dd>{formatRubles(pending.expected_cash_kopecks)}</dd></div><div><dt>Пересчёт получателя</dt><dd>{formatRubles(pending.counted_cash_kopecks)}</dd></div></dl>
            <details className="data-details"><summary>Сведения о передаче</summary><p>Редакция {pending.revision}. Подготовлена: {time(pending.prepared_at)}.</p><p>Руководитель не может подписать приём за сотрудника.</p></details>
          </section> : <section className="next-step-card" aria-label="Следующий шаг"><span className="section-symbol"><InterfaceIcon name="shift" /></span><p className="eyebrow">Следующий шаг</p><h3>{shift ? 'Подготовить сдачу' : 'Лично принять смену'}</h3><p>{shift ? 'Администратор завершает чек-лист, сохраняет кассу и выбирает принимающего.' : 'Назначенный администратор входит по PIN, проходит чек-лист и пересчитывает остаток.'}</p><a href="#current-shift" className="text-action">Перейти к смене <InterfaceIcon name="arrow" /></a><p className="next-step-note">Каждый сотрудник подтверждает действия лично.</p></section>}
        </div>
        <details className="data-details"><summary>Время и граница ответственности</summary><p>План: {time(current.planned_start_at)} — {time(current.planned_end_at)}. Фактическое начало ответственности: {time(current.actual_start_at)}.</p>{shift && !shift.overdue && <p>По плановому окончанию задержки нет.</p>}</details>
      </> : <div className="dashboard-empty"><span className="section-symbol"><InterfaceIcon name="shift" /></span><h3>Первый приём ещё не подготовлен</h3><p>Ответственный не назначен. Подготовьте черновик смены, чтобы администратор мог пройти личный приём.</p><a className="text-action" href="#current-shift">Подготовить смену <InterfaceIcon name="arrow" /></a></div>}
      <div className="dashboard-footnote"><InterfaceIcon name="shield" /><p>Тестовая денежная политика. Совпадение введённых сумм не доказывает физический остаток или отсутствие новых операций в LANGAME.</p><details className="data-details"><summary>Обновление данных</summary><p>Снимок от {time(data.observed_at)} · {data.timezone}. Фоновая проверка — каждые 30 секунд при открытой вкладке.</p></details></div>
    </>}
  </section>;
}

function ProgressCard({ label, value }: { label: string; value: Progress }) {
  return <div className="board-progress-item"><div><span>{label}</span><strong>{value.completed}<span> / {value.total}</span></strong></div><ProgressMeter label={label + ' · сохранённый прогресс'} value={value.completed} max={value.total} /></div>;
}

function CashCard({ cash, time }: { cash: Cash | null; time: (value: string | null) => string }) {
  if (!cash) return <section className="cash-overview" aria-label="Состояние кассы"><h3>Касса</h3><p>Нет сохранённой кассовой записи; это не подтверждение нулевого остатка.</p></section>;
  const metrics = [
    ['Исходный остаток', cash.opening_cash_kopecks], ['Принято лично', cash.counted_opening_kopecks],
    ['Ожидаемый остаток', cash.summary.expected_closing_kopecks], ['Пересчёт сдающего', cash.counted_closing_kopecks],
    ['Инкассации с учётом отмен', cash.summary.collection_total_kopecks], ['Расхождение', cash.summary.difference_kopecks],
  ];
  return <section className={'cash-overview cash-overview--' + cash.state} aria-label="Состояние кассы">
    <div className="workbench-heading"><span className="section-symbol"><InterfaceIcon name="reports" /></span><div><p className="eyebrow">Сохранённые данные</p><h3>Касса</h3></div></div>
    <p className="cash-state-label">{cashStates[cash.state] ?? 'Состояние не определено'}</p>
    {cash.changed_after_save && <p className="cash-stale-warning">После сохранения отчёта менялся журнал изъятий или отчёт ещё не сохраняли. Проверьте актуальность пересчёта.</p>}
    <dl className="manager-cash-grid">{metrics.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{formatRubles(value)}</dd></div>)}</dl>
    {cash.state === 'opening_unconfirmed' && <p className="snapshot-meta">Начало должен лично подтвердить назначенный администратор. Отчёт сдачи заполняется после приёма смены.</p>}
    {cash.state !== 'opening_unconfirmed' && cash.summary.blockers.length > 0 && <ul className="cash-blockers">{cash.summary.blockers.map((blocker, index) => <li key={blocker.code + ':' + index}>{blocker.message}</li>)}</ul>}
    <details className="data-details"><summary>Когда сохранены суммы</summary><p>Последнее сохранение отчёта: {time(cash.last_saved_at)}. Личный приём начального остатка: {time(cash.opening_confirmed_at)}.</p><p>Расчёт {cash.summary.policy_version} предварительный. Панель не разрешает обход кассы или чек-листа.</p></details>
  </section>;
}
