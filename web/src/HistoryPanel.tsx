import { useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, APIError } from './api';
import type { Identity, User } from './api';
import type { CashView } from './CashPanel';
import { formatRubles } from './money';

type Summary={id:string;owner_id:string;owner_name:string;business_date:string;kind:string;status:string;planned_start_at:string;planned_end_at:string;actual_start_at:string|null;actual_end_at:string|null};
type Page<T>={items:T[];next_cursor:string};
type Entry={code:string;phase:string;label:string;help_text:string;answer:string;comment:string;equipment_issue:boolean;answered_by:string|null;answered_at:string|null};
type Note={id:string;object_label:string;text:string;category:string;created_by:string;created_at:string};
type Detail={id:string;owner_id:string;owner_name:string;business_date:string;kind:string;status:string;planned_start_at:string;planned_end_at:string;actual_start_at:string|null;actual_end_at:string|null;template_version:number;checklist:Entry[];notes:Note[];cash:CashView|null};
type TransferSummary={id:string;from_shift_id:string;to_shift_id:string;from_name:string;to_name:string;revision:number;status:string;expected_cash_kopecks:string;prepared_at:string;accepted_at:string|null;cancelled_at:string|null};
type Transfer={id:string;from_user_id:string;to_user_id:string;status:string;revision:number;source_version:number;expected_cash_kopecks:string;counted_cash_kopecks:string|null;acknowledged_note_ids:string[];prepared_at:string;accepted_at:string|null;cancelled_at:string|null;actual_boundary_at:string|null;cancellation_reason:string;snapshot:Detail};
const statuses:Record<string,string>={opening:'Черновик приёма',active:'Активная смена',handover_pending:'Ожидает приёма',closed:'Закрыта',cancelled:'Отменена',pending:'Ожидает приёма',accepted:'Принята'};
const message=(cause:unknown)=>cause instanceof APIError?cause.message:'Не удалось получить данные. Повторите загрузку.';

export function HistoryPanel({identity,users,timezone}:{identity:Identity;users:User[];timezone:string}){
 const [page,setPage]=useState<Page<Summary>|null>(null);
 const [query,setQuery]=useState('limit=20');
 const [cursor,setCursor]=useState('');
 const [previous,setPrevious]=useState<string[]>([]);
 const [busy,setBusy]=useState(false);
 const [error,setError]=useState('');
 const [detail,setDetail]=useState<Detail|null>(null);
 const [transfers,setTransfers]=useState<Page<TransferSummary>|null>(null);
 const [transfer,setTransfer]=useState<Transfer|null>(null);
 const [detailError,setDetailError]=useState('');
 const [detailBusy,setDetailBusy]=useState(false);
 const [transferBusy,setTransferBusy]=useState(false);
 const listRequest=useRef(0);
 const detailRequest=useRef(0);
 const transferRequest=useRef(0);
 const time=(value:string|null)=>value?new Date(value).toLocaleString('ru-RU',{timeZone:timezone}):'Не зафиксировано';
 const name=(id:string)=>users.find(user=>user.id===id)?.display_name??id;
 async function load(nextQuery:string,nextCursor:string,back:string[]){
  const request=++listRequest.current;
  ++detailRequest.current;++transferRequest.current;setDetail(null);setTransfers(null);setTransfer(null);setDetailError('');setDetailBusy(false);setTransferBusy(false);
  setBusy(true);setError('');
  const params=new URLSearchParams(nextQuery);if(nextCursor)params.set('cursor',nextCursor);
  try{const result=await api<Page<Summary>>('/shifts?'+params.toString());if(request===listRequest.current){setPage(result);setQuery(nextQuery);setCursor(nextCursor);setPrevious(back);}}
  catch(cause){if(request===listRequest.current){setError(message(cause));setPage(null);}}
  finally{if(request===listRequest.current)setBusy(false);}
 }
 async function filter(event:FormEvent<HTMLFormElement>){
  event.preventDefault();const data=new FormData(event.currentTarget);const params=new URLSearchParams({limit:'20'});
  for(const key of ['date_from','date_to','owner_id','kind','status']){const value=String(data.get(key)??'');if(value)params.set(key,value);}
  await load(params.toString(),'',[]);
 }
 async function open(id:string){
  const request=++detailRequest.current;++transferRequest.current;
  setDetail(null);setTransfers(null);setTransfer(null);setDetailError('');setDetailBusy(true);setTransferBusy(false);
  try{
   const [shift,history]=await Promise.all([api<Detail>('/shifts/'+id),api<Page<TransferSummary>>('/shifts/'+id+'/handovers?limit=20')]);
   if(request===detailRequest.current){setDetail(shift);setTransfers(history);}
  }catch(cause){if(request===detailRequest.current)setDetailError(message(cause));}
  finally{if(request===detailRequest.current)setDetailBusy(false);}
 }
 async function moreTransfers(){
  if(!detail||!transfers?.next_cursor)return;
  const selected=detail.id,request=detailRequest.current;setTransferBusy(true);setDetailError('');
  try{const result=await api<Page<TransferSummary>>('/shifts/'+selected+'/handovers?'+new URLSearchParams({limit:'20',cursor:transfers.next_cursor}));if(request===detailRequest.current)setTransfers({items:[...transfers.items,...result.items],next_cursor:result.next_cursor});}
  catch(cause){if(request===detailRequest.current)setDetailError(message(cause));}
  finally{if(request===detailRequest.current)setTransferBusy(false);}
 }
 async function openTransfer(id:string){
  const request=++transferRequest.current,selected=detailRequest.current;setTransfer(null);setTransferBusy(true);setDetailError('');
  try{const result=await api<Transfer>('/handovers/'+id);if(request===transferRequest.current&&selected===detailRequest.current)setTransfer(result);}
  catch(cause){if(request===transferRequest.current&&selected===detailRequest.current)setDetailError(message(cause));}
  finally{if(request===transferRequest.current&&selected===detailRequest.current)setTransferBusy(false);}
 }
 return <details onToggle={event=>{if(event.currentTarget.open&&!page&&!busy&&!error)void load(query,cursor,previous);}}>
 <summary>История смен</summary><section><h2>История смен</h2>
 <p>{identity.user.roles.includes('manager')?'Доступны все смены клуба.':'Доступны ваши смены и закрытые смены, которые вы лично приняли. Снимки отозванных передач доступны через ваш отменённый черновик.'} Время: {timezone}. Данные доступны только для просмотра.</p>
 <form onSubmit={event=>void filter(event)}><fieldset disabled={busy}><legend>Фильтры истории</legend>
 <label>Дата начала от<input name="date_from" type="date"/></label><label>Дата начала до<input name="date_to" type="date"/></label>
 <label>Сотрудник истории<select name="owner_id" defaultValue=""><option value="">Все доступные</option>{users.map(user=><option key={user.id} value={user.id}>{user.display_name}{user.active?'':' (неактивен)'}</option>)}</select></label>
 <label>Тип смены<select name="kind" defaultValue=""><option value="">Любой</option><option value="day">День 09–21</option><option value="night">Ночь 21–09</option></select></label>
 <label>Статус истории<select name="status" defaultValue=""><option value="">Любой</option>{['opening','active','handover_pending','closed','cancelled'].map(status=><option key={status} value={status}>{statuses[status]}</option>)}</select></label>
 <button>Показать историю</button></fieldset></form>
 {busy&&<p role="status">Загружаем историю…</p>}{error&&<p role="alert">{error}</p>}
 {page&&!busy&&<><p>{page.items.length===0?'По выбранным фильтрам доступных смен нет.':'Смен на странице: '+page.items.length}</p><ul>{page.items.map(row=><li key={row.id}>{row.business_date} · {row.kind==='night'?'Ночь':'День'} · {row.owner_name} · {statuses[row.status]??row.status} <button onClick={()=>void open(row.id)}>Просмотреть смену {row.business_date} · {row.owner_name} · {row.kind==='night'?'Ночь':'День'}</button></li>)}</ul>
 <button disabled={previous.length===0} onClick={()=>void load(query,previous.at(-1)??'',previous.slice(0,-1))}>Предыдущая страница</button><button disabled={!page.next_cursor} onClick={()=>void load(query,page.next_cursor,[...previous,cursor])}>Следующая страница</button></>}
 {detailBusy&&<p role="status">Загружаем смену…</p>}{detailError&&<p role="alert">{detailError}</p>}
 {detail&&<section aria-label="Детали смены"><h3>Смена {detail.business_date} · {detail.owner_name||name(detail.owner_id)} · {statuses[detail.status]}</h3>
 <p>План: {time(detail.planned_start_at)} — {time(detail.planned_end_at)}. Ответственность: {time(detail.actual_start_at)} — {time(detail.actual_end_at)}.</p>
 <ChecklistSnapshot shift={detail} time={time} name={name}/><CashSnapshot shift={detail} time={time}/>
 <h4>Передачи этой смены</h4>{transfers?.items.length===0&&<p>Передач нет.</p>}<ul>{transfers?.items.map(item=><li key={item.id}>{item.from_name} → {item.to_name} · {statuses[item.status]} · редакция {item.revision} · {formatRubles(item.expected_cash_kopecks)} · {time(item.prepared_at)} <button disabled={transferBusy} onClick={()=>void openTransfer(item.id)}>Просмотреть передачу · редакция {item.revision}</button></li>)}</ul>
 {transfers?.next_cursor&&<button disabled={transferBusy} onClick={()=>void moreTransfers()}>Ещё передачи</button>}
 {transferBusy&&<p role="status">Загружаем передачу…</p>}
 {transfer&&<section aria-label="Снимок передачи"><h4>Снимок передачи · {statuses[transfer.status]} · редакция {transfer.revision}</h4>
 <p>Версия сдаваемой смены: {transfer.source_version}. Подготовлена: {time(transfer.prepared_at)}. Принята: {time(transfer.accepted_at)}. Отозвана: {time(transfer.cancelled_at)}. Граница ответственности: {time(transfer.actual_boundary_at)}.</p>
 <p>Передаваемая сумма: {formatRubles(transfer.expected_cash_kopecks)}. Пересчёт получателя: {formatRubles(transfer.counted_cash_kopecks)}.</p>{transfer.cancellation_reason&&<p>Причина отзыва: {transfer.cancellation_reason}</p>}
 <ChecklistSnapshot shift={transfer.snapshot} time={time} name={name}/><CashSnapshot shift={transfer.snapshot} time={time}/>
 <p>Ознакомление получателя: {transfer.acknowledged_note_ids.length} из {transfer.snapshot.notes.length} замечаний.</p>
 </section>}
 </section>}
 </section></details>;
}
function ChecklistSnapshot({shift,time,name}:{shift:Detail;time:(value:string|null)=>string;name:(id:string)=>string}){
 const author=(id:string)=>id===shift.owner_id?(shift.owner_name||name(id)):name(id);
 return <><p>Шаблон чек-листа: {shift.template_version}.</p>{['acceptance','closing'].map(phase=><div key={phase}><h4>{phase==='acceptance'?'Приём · сохранённые ответы':'Сдача · сохранённые ответы'}</h4><ul>{shift.checklist.filter(entry=>entry.phase===phase).map(entry=><li key={entry.code}>{entry.label}: <strong>{entry.answer==='yes'?'Да':entry.answer==='no'?'Нет':'Не заполнено'}</strong>{entry.comment&&' · '+entry.comment}{entry.equipment_issue&&' · Зафиксирована неисправность'}{entry.answered_by&&<small> · Записал: {author(entry.answered_by)} · {time(entry.answered_at)}</small>}</li>)}</ul></div>)}<h4>Сохранённые замечания</h4>{shift.notes.length===0?<p>Замечаний не записано.</p>:<ul>{shift.notes.map(note=><li key={note.id}>{note.object_label}: {note.text}<small> · Записал: {author(note.created_by)} · {time(note.created_at)}</small></li>)}</ul>}</>;
}
function CashSnapshot({shift,time}:{shift:Detail;time:(value:string|null)=>string}){
 if(!shift.cash)return <p>Принятого начального остатка ещё нет.</p>;
 const {session,summary}=shift.cash;
 return <><h4>Касса · тестовая политика {summary.policy_version}</h4><p>Расчёт предварительный; фактическую передачу подтверждают состояние и граница ответственности.</p>
 <dl><dt>Исходный остаток</dt><dd>{formatRubles(session.opening_cash_kopecks)}</dd><dt>Лично принятый пересчёт начала</dt><dd>{formatRubles(session.counted_opening_kopecks)}</dd><dt>Подтверждение приёма остатка</dt><dd>{time(session.opening_confirmed_at)}</dd><dt>Изменение наличных за смену</dt><dd>{formatRubles(session.langame_cash_amount_kopecks)}</dd><dt>Дополнительные возвраты</dt><dd>{formatRubles(session.extra_cash_refunds_kopecks)}</dd><dt>Дополнительные расходы</dt><dd>{formatRubles(session.extra_cash_expenses_kopecks)}</dd><dt>Инкассации с учётом отмен</dt><dd>{formatRubles(summary.collection_total_kopecks)}</dd><dt>Ожидаемый остаток</dt><dd>{formatRubles(summary.expected_closing_kopecks)}</dd><dt>Пересчёт сдающего</dt><dd>{formatRubles(session.counted_closing_kopecks)}</dd><dt>Расхождение</dt><dd>{formatRubles(summary.difference_kopecks)}</dd></dl>
 <ul>{session.movements.map(movement=><li key={movement.id}>{movement.kind==='reversal'?'Отмена':'Инкассация'}: {formatRubles(movement.amount_kopecks)} · {movement.reason} · {time(movement.occurred_at)}</li>)}</ul></>;
}
