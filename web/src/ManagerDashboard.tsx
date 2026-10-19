import { useEffect, useRef, useState } from 'react';
import { api, APIError } from './api';
import { formatRubles } from './money';

type Progress={completed:number;total:number;blockers:{code:string;message:string}[]};
type Cash={opening_cash_kopecks:string;counted_opening_kopecks:string|null;opening_confirmed_at:string|null;counted_closing_kopecks:string|null;state:string;last_saved_at:string|null;changed_after_save:boolean;summary:{expected_closing_kopecks:string|null;difference_kopecks:string|null;collection_total_kopecks:string|null;policy_version:string;verified:boolean;blockers:{code:string;message:string}[]}};
type Shift={id:string;owner_name:string;business_date:string;kind:string;status:string;planned_start_at:string;planned_end_at:string;actual_start_at:string|null;overdue:boolean;delay_seconds:number;acceptance:Progress;closing:Progress;cash:Cash|null};
type Dashboard={observed_at:string;timezone:string;state:string;cash_policy_status:string;production_blocked:boolean;responsible:Shift|null;initial_opening:Shift|null;pending_handover:{receipt_state:string;id:string;receiver_name:string;revision:number;prepared_at:string;expected_cash_kopecks:string;counted_cash_kopecks:string|null;acknowledged_notes:number;total_notes:number;acceptance:Progress}|null};
const cashStates:Record<string,string>={opening_unconfirmed:'Начальный остаток ещё не принят лично',incomplete:'Не все кассовые поля сохранены',mismatch:'Есть кассовое расхождение',matched:'Сохранённые суммы совпадают',invalid:'Кассовые данные требуют проверки'};
export function ManagerDashboard(){
 const [data,setData]=useState<Dashboard|null>(null);
 const [busy,setBusy]=useState(true);
 const [error,setError]=useState('');
 const reload=useRef<(()=>Promise<void>)|null>(null);
 useEffect(()=>{
  let live=true,inFlight=false;
  const load=async()=>{
   if(inFlight||!live)return;inFlight=true;setBusy(true);
   try{const result=await api<Dashboard>('/manager/dashboard');if(live){setData(result);setError('');}}
   catch(cause){if(live)setError(cause instanceof APIError?cause.message:'Не удалось связаться с сервером');}
   finally{inFlight=false;if(live)setBusy(false);}
  };
  reload.current=load;void load();
  // Passive reads do not call /auth/activity or keep the personal login alive.
  const timer=setInterval(()=>{if(!document.hidden)void load();},30_000);
  return ()=>{live=false;clearInterval(timer);if(reload.current===load)reload.current=null;};
 },[]);
 const time=(value:string|null)=>value&&data?new Date(value).toLocaleString('ru-RU',{timeZone:data.timezone}):'Не зафиксировано';
 const shift=data?.responsible;
 return <section aria-label="Панель руководителя" aria-busy={busy}>
 <h2>Панель руководителя</h2><button disabled={busy} onClick={()=>void reload.current?.()}>Обновить панель</button>
 {busy&&<p role="status">Получаем состояние клуба…</p>}
 {error&&<p role="alert">Ошибка обновления: {error}. {data?'Ниже сохранённый снимок; он может быть неактуален.':'Состояние клуба неизвестно.'}</p>}
 {data&&<><p>Снимок от {time(data.observed_at)} · {data.timezone}. Фоновая проверка — каждые 30 секунд при открытой вкладке.</p>
 <p>Тестовая денежная политика. Совпадение введённых сумм не доказывает физический остаток или отсутствие новых операций в LANGAME.</p>
 {data.production_blocked&&<p>Рабочие операции production заблокированы до проверки кассовой политики.</p>}
 {data.state==='uninitialized'&&<p>Первый приём ещё не подготовлен. Ответственный не назначен.</p>}
 {data.state==='no_responsible'&&<p role="alert">В истории есть смены, но подтверждённого ответственного сейчас нет. Требуется проверка.</p>}
 {!shift&&data.initial_opening&&<p>Ответственный ещё не назначен: первый черновик должен быть лично принят.</p>}
 {data.initial_opening&&<><h3>Первый черновик · {data.initial_opening.owner_name}</h3><p>Приём: {data.initial_opening.acceptance.completed} из {data.initial_opening.acceptance.total}. Подготовка черновика не назначает ответственность.</p><CashCard cash={data.initial_opening.cash} time={time}/></>}
 {shift&&<><h3>Ответственный: {shift.owner_name}</h3><p>{shift.business_date} · {shift.kind==='night'?'Ночь 21–09':'День 09–21'} · {shift.status==='handover_pending'?'Ожидает приёма; ответственность сохраняется':'Активная смена'}</p>
 <p>План: {time(shift.planned_start_at)} — {time(shift.planned_end_at)}. Фактическое начало ответственности: {time(shift.actual_start_at)}.</p>
 {shift.overdue?<p role="alert">Передача задержана на {Math.floor(shift.delay_seconds/60)} мин. Ответственным остаётся {shift.owner_name}.</p>:<p>По плановому окончанию задержки нет.</p>}
 <p>Приём: {shift.acceptance.completed} из {shift.acceptance.total}. Сдача: {shift.closing.completed} из {shift.closing.total}.</p>
 <ul>{[...shift.acceptance.blockers,...shift.closing.blockers].map((blocker,index)=><li key={blocker.code+':'+index}>{blocker.message}</li>)}</ul>
 <CashCard cash={shift.cash} time={time}/>
 </>}
 {data.pending_handover&&<section aria-label="Ожидающая передача"><h3>Передача ожидает приёма</h3><p>Принимающий: {data.pending_handover.receiver_name}. Редакция {data.pending_handover.revision}. Подготовлена: {time(data.pending_handover.prepared_at)}.</p>
 <p>Сумма к передаче: {formatRubles(data.pending_handover.expected_cash_kopecks)}. Пересчёт получателя: {formatRubles(data.pending_handover.counted_cash_kopecks)}.</p>
 <p>{data.pending_handover.receipt_state==='missing'?'Пересчёт получателя ещё не сохранён':data.pending_handover.receipt_state==='mismatch'?'Пересчёт получателя не совпадает; приём заблокирован':data.pending_handover.receipt_state==='matched'?'Сохранённый пересчёт получателя совпадает с передаваемой суммой':'Состояние пересчёта получателя неизвестно'}.</p>
 <p>Чек-лист получателя: {data.pending_handover.acceptance.completed} из {data.pending_handover.acceptance.total}. Ознакомление: {data.pending_handover.acknowledged_notes} из {data.pending_handover.total_notes} замечаний.</p>
 <p>Подготовка передачи не снимает ответственность со сдающего. Руководитель не может подписать приём за сотрудника.</p></section>}
 </>}
 </section>;
}
function CashCard({cash,time}:{cash:Cash|null;time:(value:string|null)=>string}){
 if(!cash)return <p>Нет сохранённой кассовой записи; это не подтверждение нулевого остатка.</p>;
 return <section aria-label="Состояние кассы"><h3>Касса: {cashStates[cash.state]??'Состояние не определено'}</h3>
 <p>Последнее сохранение отчёта: {time(cash.last_saved_at)}. Личный приём начального остатка: {time(cash.opening_confirmed_at)}.</p>
 {cash.changed_after_save&&<p>После сохранения отчёта менялся журнал изъятий или отчёт ещё не сохраняли. Проверьте актуальность пересчёта.</p>}
 <dl><dt>Исходный остаток</dt><dd>{formatRubles(cash.opening_cash_kopecks)}</dd><dt>Лично принятый пересчёт начала</dt><dd>{formatRubles(cash.counted_opening_kopecks)}</dd><dt>Ожидаемый остаток</dt><dd>{formatRubles(cash.summary.expected_closing_kopecks)}</dd><dt>Пересчёт сдающего</dt><dd>{formatRubles(cash.counted_closing_kopecks)}</dd><dt>Расхождение</dt><dd>{formatRubles(cash.summary.difference_kopecks)}</dd><dt>Инкассации с учётом отмен</dt><dd>{formatRubles(cash.summary.collection_total_kopecks)}</dd></dl>
 <p>{cash.state==='opening_unconfirmed'?'Начало должен лично подтвердить назначенный администратор. Отчёт сдачи заполняется после приёма смены.':'Проверка выполнена по сохранённым данным.'}</p>
 {cash.state!=='opening_unconfirmed'&&<ul>{cash.summary.blockers.map((blocker,index)=><li key={blocker.code+':'+index}>{blocker.message}</li>)}</ul>}
 <p>Расчёт {cash.summary.policy_version} предварительный. Панель не разрешает обход кассы или чек-листа.</p></section>;
}
