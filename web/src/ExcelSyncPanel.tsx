import { useEffect, useState } from 'react';
import { api } from './api';

type State = { requested_version: number; synced_version: number; last_error_code: string; last_success_at: string | null };
const messages: Record<string,string> = {
  EXCEL_AUTH_REQUIRED:'Проверьте доступ технического аккаунта Google.',
  EXCEL_FILE_ACCESS:'Нет доступа к Excel-файлу или указан другой тип файла.',
  EXCEL_TEMPLATE_MISMATCH:'Столбцы Excel изменились. Нужно проверить соответствие шаблону.',
  EXCEL_ROW_EDITED:'Строку изменили вручную. Автозаполнение остановлено, чтобы сохранить правку.',
  EXCEL_VERSION_CONFLICT:'Версия строки в Excel требует проверки.',
  EXCEL_DUPLICATE_SHIFT:'В Excel обнаружены две строки одной смены. Нужна проверка.',
  EXCEL_ETAG_REQUIRED:'Google не предоставил защиту от одновременной правки. Файл не перезаписан.',
  EXCEL_FILE_TOO_LARGE:'Excel-файл превысил допустимый размер для синхронизации.',
  EXCEL_TEXT_TOO_LONG:'Замечания не помещаются в ячейку Excel. Полные данные сохранены в приложении.',
  EXCEL_INVALID_WORKBOOK:'Не удалось прочитать Excel-файл.',
  EXCEL_ROW_LIMIT:'Превышен допустимый размер листа Excel.',
};

export function ExcelSyncPanel({shiftID,version,enabled}:{shiftID:string;version:number;enabled:boolean}) {
  const [state,setState]=useState<State|null>(null);
  const [unavailable,setUnavailable]=useState(false);
  useEffect(()=>{
    if (!enabled) return;
    let live=true;
    let inFlight=false;
    const load=async()=>{
      if (document.hidden || inFlight) return;
      inFlight=true;
      try { const result=await api<State>('/shifts/'+shiftID+'/excel-sync');if(live){setState(result);setUnavailable(false);} }
      catch { if(live) setUnavailable(true); }
      finally { inFlight=false; }
    };
    void load();
    // Read-only polling never calls /auth/activity or extends the personal session.
    const timer=setInterval(()=>void load(),10_000);
    return ()=>{live=false;clearInterval(timer);};
  },[shiftID,version,enabled]);
  let message='Автозаполнение Excel не подключено. Ответы сохранены в приложении.';
  if(enabled){
    if(unavailable) message='Не удалось проверить отправку в Excel. Обновите страницу или войдите заново.';
    else if(!state) message='Проверяем отправку в Excel…';
    else if(state.last_error_code) message=messages[state.last_error_code]??'Обновление Excel ожидает повторной отправки. Данные сохранены в приложении.';
    else if(state.last_success_at && state.synced_version>=Math.max(state.requested_version,version)) message='Сохранённые ответы отправлены в Excel.';
    else message='Обновление Excel ожидает отправки.';
  }
  return <aside><h3>Автозаполнение Excel</h3><p role="status">{message}</p><p>В файл попадают только сохранённые ответы. Отправка в Excel не означает завершение приёма или передачи смены. Сейчас поддерживается тестовый чек-лист.</p></aside>;
}
