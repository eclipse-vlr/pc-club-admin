import { useState } from 'react';
import type { FormEvent } from 'react';
import { APIError, downloadCSV } from './api';
import type { User } from './api';

export function ExportPanel({users}:{users:User[]}){
 const [busy,setBusy]=useState(false);
 const [error,setError]=useState('');
 const [message,setMessage]=useState('');
 async function submit(event:FormEvent<HTMLFormElement>){
  event.preventDefault();const form=new FormData(event.currentTarget);
  const params=new URLSearchParams({date_from:String(form.get('date_from')),date_to:String(form.get('date_to'))});
  const owner=String(form.get('owner_id')??'');if(owner)params.set('owner_id',owner);
  setError('');setMessage('');
  if(params.get('date_from')!>params.get('date_to')!){setError('Начало периода должно быть не позже конца.');return;}
  setBusy(true);
  try{await downloadCSV(form.get('kind')==='cash'?'cash':'shifts',params);setMessage('Файл передан браузеру для скачивания.');}
  catch(cause){setError(cause instanceof APIError?cause.message:'Не удалось получить файл. Повторите запрос.');}
  finally{setBusy(false);}
 }
 return <details><summary>Выгрузки CSV</summary><section aria-label="Выгрузки CSV"><h2>Выгрузки CSV</h2>
  <p>Только тестовые данные: денежная политика не проверена. Период включает даты начала смены по времени клуба. Не более 5000 смен в одном файле; при превышении уменьшите период.</p>
  <p>CSV содержит значения без формул, разделитель — точка с запятой, суммы — в рублях с точкой. Пустая сумма означает отсутствие данных. Кассовая выгрузка содержит введённые значения и инкассации с учётом отмен; это не утверждённый финансовый отчёт.</p>
  <form onSubmit={event=>void submit(event)}><fieldset disabled={busy}><legend>Параметры выгрузки</legend>
   <label>Выгрузка<select name="kind" aria-label="Вид выгрузки"><option value="shifts">Смены</option><option value="cash">Касса</option></select></label>
   <label>Дата смены от<input type="date" name="date_from" required/></label><label>Дата смены до<input type="date" name="date_to" required/></label>
   <label>Сотрудник выгрузки<select name="owner_id" defaultValue=""><option value="">Все сотрудники</option>{users.map(user=><option key={user.id} value={user.id}>{user.display_name}{user.active?'':' (неактивен)'}</option>)}</select></label>
   <button>Скачать CSV</button>
  </fieldset></form>
  {busy&&<p role="status">Формируем выгрузку…</p>}{error&&<p role="alert">{error}</p>}{message&&<p role="status">{message}</p>}
 </section></details>;
}
