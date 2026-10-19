import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, APIError } from './api';
import type { Identity, User } from './api';
import { formatRubles, parseRubles, stringifyExact } from './money';

type Shift = {id:string;owner_id:string;version:number;status:string;handover_id:string;handover_parent_id:string};
type Handover = {
 id:string;from_user_id:string;to_user_id:string;to_shift_id:string;revision:number;version:number;status:string;
 expected_cash_kopecks:string;counted_cash_kopecks:string|null;acknowledged_note_ids:string[];
 snapshot:{notes:{id:string;object_label:string;text:string}[];checklist:{code:string;phase:string;label:string;answer:string;comment:string}[]};
};
export function HandoverPanel({identity,shift,users,canPrepare,refresh,onSessionEnd}:{identity:Identity;shift:Shift;users:User[];canPrepare:boolean;refresh:()=>Promise<void>;onSessionEnd:()=>Promise<void>}){
 const [handover,setHandover]=useState<Handover|null>(null);
 const [error,setError]=useState('');
 const [busy,setBusy]=useState(false);
 const [uncertain,setUncertain]=useState(false);
 const attempt=useRef<{route:string;body:string;key:string}|null>(null);
 const actor=identity.user.id;
 const own=identity.pin_session&&identity.device_trusted&&shift.owner_id===actor;
 useEffect(()=>{
  if(!shift.handover_id)return;
  let live=true;
  void api<Handover>('/handovers/'+shift.handover_id).then(result=>{if(live)setHandover(result);}).catch(()=>{if(live)setError('Передача не загружена. Перечитайте данные.');});
  return ()=>{live=false;};
 },[shift.handover_id]);
 async function send(route:string,method:string,body:unknown){
  const canonical=stringifyExact(body);
  if(attempt.current?.route!==route||attempt.current.body!==canonical)attempt.current={route,body:canonical,key:crypto.randomUUID()};
  return api<Handover>(route,method,body,actor,attempt.current.key);
 }
 async function proof(pin:string,action:string,target:string,version:number){return (await api<{proof:string}>('/auth/reauth','POST',{pin,action,target_id:target,revision:version},actor)).proof;}
 async function submit(event:FormEvent<HTMLFormElement>,work:(data:FormData)=>Promise<void>){
  event.preventDefault();const form=event.currentTarget;const data=new FormData(form);
  form.querySelectorAll<HTMLInputElement>('input[type="password"]').forEach(input=>{input.value='';});
  setBusy(true);setError('');
  try{await work(data);}
  catch(cause){setError(cause instanceof APIError?cause.message:cause instanceof Error?cause.message:'Не удалось выполнить действие');setUncertain(true);}
  finally{setBusy(false);}
 }
 const text=(data:FormData,key:string)=>String(data.get(key)??'');
 const pin=<label>Мой личный PIN<input name="pin" type="password" inputMode="numeric" pattern="[0-9]{6}" maxLength={6} required autoComplete="off"/></label>;
 const disabled=busy||uncertain;
 return <section><h3>Передача смены · Тестовый режим</h3>
 <p>До окончательного приёма ответственным остаётся сдающий. Каждый сотрудник входит лично на этом же зарегистрированном браузере.</p>
 {error&&<p role="alert">{error} Результат мог сохраниться. Перечитайте данные перед повтором.</p>}
 <button disabled={busy} onClick={()=>void onSessionEnd().then(refresh).catch(()=>setError('Не удалось проверить сессию и состояние. Обновите страницу.'))}>Проверить сессию и перечитать передачу</button>
 {own&&shift.status==='active'&&<form onSubmit={event=>void submit(event,async data=>{
  const token=await proof(text(data,'pin'),'prepare_handover',shift.id,shift.version);
  await send('/shifts/'+shift.id+'/handovers','POST',{receiver_id:text(data,'receiver'),version:shift.version,reauth_proof:token});
  await onSessionEnd();
 })}><h4>Подготовить сдачу</h4><p>Заполните сдачу и сверьте кассу. После подготовки ваша сессия завершится; принимающий должен войти под своим именем. Новые внешние платежи требуют отзыва передачи и обновления отчёта.</p>
 <label>Принимающий<select name="receiver" defaultValue="" required><option value="" disabled>Выберите сотрудника</option>{users.filter(user=>user.active&&user.roles.includes('admin')&&user.id!==actor).map(user=><option key={user.id} value={user.id}>{user.display_name}</option>)}</select></label>
 {pin}<button disabled={disabled||!canPrepare}>Подготовить передачу и выйти</button></form>}
 {shift.handover_id&&!handover&&<p>Загружаем снимок передачи…</p>}
 {handover&&<><p>Редакция {handover.revision} · {handover.status==='pending'?'Ожидает приёма':handover.status==='accepted'?'Принята':'Отозвана'}</p>
 <p>Передаваемая сумма: {formatRubles(handover.expected_cash_kopecks)}. Сохранённый пересчёт получателя: {formatRubles(handover.counted_cash_kopecks)}.</p>
 <details><summary>Ответы сдающего</summary><ul>{handover.snapshot.checklist.filter(entry=>entry.phase==='closing').map(entry=><li key={entry.code}>{entry.label}: {entry.answer==='yes'?'Да':entry.answer==='no'?'Нет':'Не заполнено'} {entry.comment}</li>)}</ul></details>
 {handover.snapshot.notes.length===0&&<p>У сдающего нет записанных замечаний.</p>}
 {handover.status==='pending'&&actor===handover.from_user_id&&identity.pin_session&&<form onSubmit={event=>void submit(event,async data=>{await send('/handovers/'+handover.id+'/cancel','POST',{version:handover.version,reason:text(data,'reason')});await refresh();})}><label>Причина отзыва<textarea name="reason" required maxLength={4000}/></label><button disabled={disabled}>Отозвать и вернуться к своей смене</button></form>}
 {handover.status==='pending'&&actor===handover.to_user_id&&own&&<>
 <p>Заполните 12 пунктов своего приёма выше. Сумма станет вашим начальным остатком только после окончательного подтверждения.</p>
 <form onSubmit={event=>void submit(event,async data=>{const amount=parseRubles(text(data,'counted'));if(amount<0n)throw new Error('Пересчёт не может быть отрицательным');await send('/handovers/'+handover.id+'/receipt','PATCH',{revision:handover.revision,version:handover.version,counted_cash_kopecks:amount});await refresh();})}><label>Мой пересчёт наличных · рубли<input name="counted" inputMode="decimal" required/></label><button disabled={disabled}>Сохранить мой пересчёт</button></form>
 <form onSubmit={event=>void submit(event,async data=>{await send('/handovers/'+handover.id+'/acknowledgements','POST',{revision:handover.revision,version:handover.version,note_ids:data.getAll('notes').map(String)});await refresh();})}>
 {handover.snapshot.notes.map(note=><label key={note.id}><input className="inline" type="checkbox" name="notes" value={note.id} defaultChecked={handover.acknowledged_note_ids.includes(note.id)} disabled={handover.acknowledged_note_ids.includes(note.id)}/>{note.object_label}: {note.text} — ознакомился</label>)}
 {handover.snapshot.notes.length>0&&<button disabled={disabled}>Сохранить ознакомление</button>}</form>
 <form onSubmit={event=>void submit(event,async data=>{const token=await proof(text(data,'pin'),'accept_handover',handover.id,shift.version);await send('/handovers/'+handover.id+'/accept','POST',{revision:handover.revision,version:handover.version,shift_version:shift.version,reauth_proof:token});await refresh();})}>{pin}<button disabled={disabled}>Лично принять смену</button></form>
 </>}
 </>}
 </section>;
}
