import { useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { formatRubles } from './money';
import { api, APIError } from './api';
import type { User } from './api';

type AuditEvent = {
 id: string; actor_id: string|null; actor_name: string|null; device_id: string|null;
 changes:{field:string;before:string|null;after:string|null}[];
 action: string; entity_id: string; request_id: string; success: boolean; reason: string; created_at: string;
};
type AuditPage = {items: AuditEvent[]; next_cursor: string};
const fields:Record<string,string>={id:"Запись (ID)",kind:"Тип",amount_kopecks:"Сумма",reversal_of:"Отмена записи",reason:"Основание",recipient:"Получатель",occurred_at:"Время операции",actor_id:"Автор (ID)",owner_id:"Ответственный (ID)",from_shift_id:"Сдаваемая смена (ID)",to_shift_id:"Принимаемая смена (ID)",revision:"Редакция",expected_cash_kopecks:"Передаваемая сумма",counted_cash_kopecks:"Пересчёт получателя",status:"Статус",cancellation_reason:"Причина отзыва",actual_boundary_at:"Граница ответственности",accepted_opening_kopecks:"Принятый остаток",template_version:"Версия шаблона",cash:"Кассовая выгрузка",date_from:"Дата от",date_to:"Дата до",row_count:"Число строк",display_name:"Имя",active:"Доступ включён",version:"Версия",answer:"Ответ",comment:"Комментарий",equipment_issue:"Поломка",checklist_code:"Пункт",category:"Категория",object_label:"Объект",text:"Текст",opening_cash_kopecks:"Начальный остаток",counted_opening_kopecks:"Пересчёт при приёме",langame_cash_amount_kopecks:"Операции LANGAME",extra_cash_refunds_kopecks:"Дополнительные возвраты",extra_cash_expenses_kopecks:"Дополнительные расходы",counted_closing_kopecks:"Пересчёт при сдаче",policy_version:"Версия денежной политики"};
const value=(field:string,text:string|null)=>{
 if(text===null)return 'Не записано';
 if(field.endsWith('_kopecks'))return formatRubles(text);
 if(['active','equipment_issue','cash'].includes(field))return text==='true'?'Да':'Нет';
 if(field==='answer')return ({yes:'Да',no:'Нет',unanswered:'Не заполнено'}[text]??text);
 return text;
};
const actions: Record<string,string> = {
 user_updated:"Изменение сотрудника",pin_reset:"Сброс PIN",checklist_answered:"Ответ чек-листа",shift_note_saved:"Сохранение заметки",
 cash_saved: 'Сохранение кассы', shift_request_rejected: 'Отклонённое действие со сменой',
 handover_prepared: 'Подготовка передачи', handover_accepted: 'Приём передачи',
 handover_cancelled: 'Отзыв передачи', handover_receipt_saved: 'Пересчёт получателя',
 handover_notes_acknowledged: 'Ознакомление с замечаниями', excel_synchronization: 'Синхронизация Excel',
};

export function AuditPanel({users,timezone}:{users:User[];timezone:string}) {
 const [page,setPage]=useState<AuditPage|null>(null);
 const [query,setQuery]=useState('limit=20');
 const [cursor,setCursor]=useState('');
 const [previous,setPrevious]=useState<string[]>([]);
 const [busy,setBusy]=useState(false);
 const [error,setError]=useState('');
 const request=useRef(0);
 const localZone=Intl.DateTimeFormat().resolvedOptions().timeZone;
 async function load(nextQuery:string,nextCursor:string,back:string[]) {
  const sequence=++request.current;setBusy(true);setError('');setPage(null);
  const params=new URLSearchParams(nextQuery);
  if(nextCursor)params.set('cursor',nextCursor);
  try {
   const result=await api<AuditPage>('/audit-events?'+params.toString());
   if(sequence===request.current){setPage(result);setQuery(nextQuery);setCursor(nextCursor);setPrevious(back);}
  } catch(cause) {
   if(sequence===request.current)setError(cause instanceof APIError?cause.message:'Не удалось загрузить журнал. Повторите запрос.');
  } finally {if(sequence===request.current)setBusy(false);}
 }
 function filter(event:FormEvent<HTMLFormElement>) {
  event.preventDefault();const data=new FormData(event.currentTarget);
  const params=new URLSearchParams({limit:'20'});
  for(const key of ['actor_id','entity_id','action']) {
   const value=String(data.get(key)??'').trim();if(value)params.set(key,value);
  }
  for(const key of ['from','to']) {
   const value=String(data.get(key)??'');
   if(value){const parsed=new Date(value);if(Number.isNaN(parsed.getTime())){setError('Проверьте время периода.');return;}params.set(key,parsed.toISOString());}
  }
  if(params.has('from')&&params.has('to')&&params.get('from')!>=params.get('to')!){setError('Конец периода должен быть позже начала.');return;}
  void load(params.toString(),'',[]);
 }
 return <details onToggle={event=>{if(event.currentTarget.open&&!page&&!busy&&!error)void load(query,cursor,previous);}}>
  <summary>Журнал действий</summary>
  <section aria-label="Журнал действий"><h2>Журнал действий</h2>
   <p>Доступен только управляющему. События показаны от новых к старым. Изменять записи нельзя.</p>
   <p>Время событий: {timezone}. Период вводится во времени браузера: {localZone}; начало включается, конец не включается.</p>
   <form onSubmit={filter}><fieldset disabled={busy}><legend>Фильтры журнала</legend>
    <label>Автор события<select name="actor_id" defaultValue=""><option value="">Все авторы, включая систему</option>{users.map(user=><option key={user.id} value={user.id}>{user.display_name}{user.active?'':' (неактивен)'}</option>)}</select></label>
    <label>Начало периода<input name="from" type="datetime-local" step="1"/></label>
    <label>Конец периода<input name="to" type="datetime-local" step="1"/></label>
    <label>Действие (точный код)<input name="action" maxLength={100} list="audit-actions"/></label>
    <datalist id="audit-actions">{Object.entries(actions).map(([code,label])=><option key={code} value={code}>{label}</option>)}</datalist>
    <label>Объект (точный ID)<input name="entity_id" maxLength={256}/></label>
    <button>Показать журнал</button>
   </fieldset></form>
   <button disabled={busy} onClick={()=>void load(query,'',[])}>Обновить журнал</button>
   {busy&&<p role="status">Загружаем журнал…</p>}{error&&<p role="alert">{error}</p>}
   {page&&!busy&&<>
    <p>{page.items.length?'Событий на странице: '+page.items.length:'По выбранным фильтрам событий нет.'}</p>
    <ol>{page.items.map(item=><li key={item.id}>
     <p>{new Date(item.created_at).toLocaleString('ru-RU',{timeZone:timezone})} · {item.actor_id?(item.actor_name??item.actor_id):'Система / автор не указан'} · {actions[item.action]??item.action} · {item.success?'Успешно':'Отклонено'}</p>
     <details><summary>Сведения о событии {item.id}</summary><dl>
      <dt>Код действия</dt><dd>{item.action}</dd><dt>Объект</dt><dd>{item.entity_id||'Не указан'}</dd>
      <dt>Автор (ID)</dt><dd>{item.actor_id??'Не указан'}</dd><dt>Устройство</dt><dd>{item.device_id??'Не указано'}</dd>
      <dt>Запрос</dt><dd>{item.request_id||'Не указан'}</dd><dt>Причина</dt><dd>{item.reason||'Не указана'}</dd>
     </dl>{item.changes?.length>0?<table><caption>Изменения</caption><thead><tr><th>Поле</th><th>До</th><th>После</th></tr></thead><tbody>{item.changes.map(change=><tr key={change.field}><th>{fields[change.field]??change.field}</th><td>{value(change.field,change.before)}</td><td>{value(change.field,change.after)}</td></tr>)}</tbody></table>:<p>Для этого события изменения полей не записаны или не представлены.</p>}</details>
    </li>)}</ol>
    <button disabled={!previous.length} onClick={()=>void load(query,previous.at(-1)??'',previous.slice(0,-1))}>Предыдущие события</button>
    <button disabled={!page.next_cursor} onClick={()=>void load(query,page.next_cursor,[...previous,cursor])}>Следующие события</button>
   </>}
  </section>
 </details>;
}
