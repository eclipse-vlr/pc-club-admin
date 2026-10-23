import { useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { api, APIError } from './api';
import type { User } from './api';

export function EmployeePanel({users,actor,refresh}:{users:User[];actor:string;refresh:()=>Promise<void>}) {
 const [busy,setBusy]=useState(false);
 const [error,setError]=useState('');
 const [message,setMessage]=useState('');
 const [uncertain,setUncertain]=useState(false);
 const [generation,setGeneration]=useState(0);
 const inFlight=useRef(false);
 // A failed write or reload must be reconciled before another write starts.
 const needsRead=useRef(false);
 async function save(event:FormEvent<HTMLFormElement>,user:User,pin=false){
  event.preventDefault();
  if(inFlight.current||needsRead.current)return;
  inFlight.current=true;
  const form=event.currentTarget;const data=new FormData(form);
  // Never retain a PIN in a form after submission, including a failed request.
  if(pin)form.reset();
  setBusy(true);setError('');setMessage('');
  try{
   if(pin)await api('/users/'+user.id+'/reset-pin','POST',{pin:String(data.get('pin')??''),version:user.version},actor);
   else await api('/users/'+user.id,'PATCH',{display_name:String(data.get('name')??''),active:data.get('active')==='on',version:user.version},actor);
   await refresh();setMessage(pin?'PIN изменён. Прежние сессии сотрудника завершены.':'Данные сотрудника сохранены.');
  }catch(cause){
   setError(cause instanceof APIError?cause.message:'Связь прервалась. Перечитайте сотрудников перед повтором.');
   needsRead.current=true;
   setUncertain(true);
  }finally{inFlight.current=false;setBusy(false);}
 }
 async function reload(){
  if(inFlight.current)return;
  inFlight.current=true;setBusy(true);setError('');setMessage('');
  try{await refresh();needsRead.current=false;setUncertain(false);setGeneration(value=>value+1);}
  catch{needsRead.current=true;setUncertain(true);setError('Не удалось перечитать сотрудников. Повторите загрузку.');}
  finally{inFlight.current=false;setBusy(false);}
 }
 return <section aria-label="Управление сотрудниками"><h3>Изменение сотрудников</h3>
  <p>Отключение завершает сессии. Нельзя отключить последнего управляющего или сотрудника с незавершённой сменой. Сброс PIN завершает прежние сессии, но не передаёт ответственность за смену.</p>
  {error&&<p role="alert">{error}</p>}{message&&<p role="status">{message}</p>}
  <button disabled={busy} onClick={()=>void reload()}>Перечитать сотрудников</button>
  {users.map(user=><details key={user.id+':'+user.version+':'+generation}><summary>Изменить: {user.display_name}</summary>
   <form onSubmit={event=>void save(event,user)}><fieldset disabled={busy||uncertain}><legend>Данные: {user.display_name}</legend>
    <label>Имя сотрудника<input name="name" required maxLength={100} defaultValue={user.display_name}/></label>
    <label><input name="active" type="checkbox" defaultChecked={user.active}/>Доступ сотрудника включён</label>
    <button>Сохранить сотрудника</button>
   </fieldset></form>
   {user.roles.includes('admin')&&<form onSubmit={event=>void save(event,user,true)}><fieldset disabled={busy||uncertain}><legend>Новый PIN: {user.display_name}</legend>
    <label>Новый личный PIN<input name="pin" type="password" inputMode="numeric" pattern="[0-9]{6}" minLength={6} maxLength={6} required autoComplete="new-password"/></label>
    <button>Сбросить PIN</button>
   </fieldset></form>}
  </details>)}
 </section>;
}
