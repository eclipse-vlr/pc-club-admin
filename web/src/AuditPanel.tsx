import { useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, APIError } from './api';
import type { User } from './api';

type AuditEvent = {
 id: string; actor_id: string|null; actor_name: string|null; device_id: string|null;
 action: string; entity_id: string; request_id: string; success: boolean; reason: string; created_at: string;
};
type AuditPage = {items: AuditEvent[]; next_cursor: string};
const actions: Record<string,string> = {
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
     </dl></details>
    </li>)}</ol>
    <button disabled={!previous.length} onClick={()=>void load(query,previous.at(-1)??'',previous.slice(0,-1))}>Предыдущие события</button>
    <button disabled={!page.next_cursor} onClick={()=>void load(query,page.next_cursor,[...previous,cursor])}>Следующие события</button>
   </>}
  </section>
 </details>;
}
