import { useEffect, useState } from 'react';
import { api } from './api';
import { excelDeliveryView, excelSuccessTime, safeExcelURL } from './excel-status';
import type { ExcelState } from './excel-status';

export function ExcelSyncPanel({ shiftID, version, enabled, unsavedCount = 0, saving = false, saveUncertain = false, needsStorageRead = false }: { shiftID: string; version: number; enabled: boolean; unsavedCount?: number; saving?: boolean; saveUncertain?: boolean; needsStorageRead?: boolean }) {
  const [state, setState] = useState<ExcelState | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [reload, setReload] = useState(0);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!enabled) return;
    let live = true;
    let inFlight = false;
    const load = async () => {
      if (document.hidden || inFlight || !live) return;
      inFlight = true;
      setBusy(true);
      try {
        const result = await api<ExcelState>('/shifts/' + shiftID + '/excel-sync');
        if (live) { setState(result); setUnavailable(false); }
      } catch { if (live) setUnavailable(true); }
      finally { inFlight = false; if (live) setBusy(false); }
    };
    void load();
    // Passive reads never call /auth/activity or extend the personal session.
    const timer = setInterval(() => void load(), 10_000);
    return () => { live = false; clearInterval(timer); };
  }, [shiftID, version, enabled, reload]);

  const view = excelDeliveryView(state, version, enabled, unavailable);
  const visible = enabled && !unavailable ? state : null;
  const fileURL = safeExcelURL(visible?.file_url);
  const lastSuccess = visible ? excelSuccessTime(visible.last_success_at, visible.timezone) : null;
  const localChanges = unsavedCount > 0 || saving || saveUncertain || needsStorageRead;
  const currentSent = view.sent && !localChanges;
  const storageMessage = saving ? 'Сохраняем изменения в приложении…'
    : needsStorageRead ? 'Запись сохранена в приложении. Перечитайте актуальную версию смены перед новой операцией.'
    : saveUncertain ? 'Результат сохранения неизвестен. Перечитайте данные; повторять отправку вслепую нельзя.'
    : unsavedCount ? `Есть несохранённые изменения: ${unsavedCount}. Сохраните ответы и замечания.`
    : 'Ответы сохранены в приложении.';
  return <aside className={'excel-status ' + (view.sent && localChanges ? 'is-pending' : view.tone)} aria-label="Отправка чек-листа в Excel">
    <h3>Автозаполнение Excel</h3>
    <div className="save-destinations">
      <div role="group" aria-label="Сохранение в приложении" className={localChanges ? 'storage-pending' : 'storage-saved'}><h4>В приложении</h4><p role="status">{storageMessage}</p><p className="excel-meta">Статус ответов и замечаний чек-листа.</p></div>
      <div role="group" aria-label="Отправка в Excel"><h4>В Excel</h4><p role="status">{view.sent && localChanges ? 'Предыдущая сохранённая версия отправлена в Excel. Текущие правки ещё не подтверждены в таблице.' : view.message}</p></div>
    </div>
    {visible && <p className="excel-meta">{visible.last_success_at ? <>{currentSent ? 'Последняя успешная отправка' : 'Предыдущая успешная отправка'}: {lastSuccess ? <time dateTime={visible.last_success_at}>{lastSuccess} · время клуба</time> : 'время недоступно'}.{!currentSent && ' Новые изменения ещё не подтверждены.'}</> : 'Успешных отправок этой смены пока нет.'}</p>}
    {fileURL && <><a className="excel-link" href={fileURL} target="_blank" rel="noopener noreferrer" aria-label="Открыть тестовый Excel в новой вкладке">Открыть тестовый Excel</a><p className="excel-meta">Лист «{visible?.sheet_name}». Ищите строку по дате смены и имени ответственного.</p></>}
    {enabled && <button className="excel-refresh" disabled={busy} onClick={() => setReload(value => value + 1)}>Обновить статус Excel</button>}
    <p>В файл попадают только сохранённые ответы. Отправка в Excel не означает завершение приёма или передачи смены. Сейчас поддерживается тестовый чек-лист.</p>
  </aside>;
}
