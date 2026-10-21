export type ExcelState = {
  requested_version: number;
  synced_version: number;
  last_error_code: string;
  last_success_at: string | null;
  file_url?: string;
  sheet_name?: string;
  timezone: string;
};

const messages: Record<string, string> = {
  EXCEL_AUTH_REQUIRED: 'Проверьте доступ технического аккаунта Google.',
  EXCEL_FILE_ACCESS: 'Нет доступа к Excel-файлу или указан другой тип файла.',
  EXCEL_TEMPLATE_MISMATCH: 'Столбцы Excel изменились. Нужно проверить соответствие шаблону.',
  EXCEL_ROW_EDITED: 'Строку изменили вручную. Автозаполнение остановлено, чтобы сохранить правку.',
  EXCEL_VERSION_CONFLICT: 'Версия строки в Excel требует проверки.',
  EXCEL_DUPLICATE_SHIFT: 'В Excel обнаружены две строки одной смены. Нужна проверка.',
  EXCEL_ETAG_REQUIRED: 'Google не предоставил защиту от одновременной правки. Файл не перезаписан.',
  EXCEL_FILE_TOO_LARGE: 'Excel-файл превысил допустимый размер для синхронизации.',
  EXCEL_TEXT_TOO_LONG: 'Замечания не помещаются в ячейку Excel. Полные данные сохранены в приложении.',
  EXCEL_INVALID_WORKBOOK: 'Не удалось прочитать Excel-файл.',
  EXCEL_ROW_LIMIT: 'Превышен допустимый размер листа Excel.',
  EXCEL_REMOTE_CONFLICT: 'Файл изменился во время отправки. Сервис перечитает его перед повтором.',
  EXCEL_NETWORK_UNAVAILABLE: 'Нет связи с Drive. Ответы сохранены в приложении и ожидают отправки.',
  EXCEL_PROVIDER_UNAVAILABLE: 'Drive временно недоступен. Отправка повторится автоматически.',
  EXCEL_RATE_LIMIT: 'Google временно ограничил частоту запросов. Отправка повторится позже.',
  EXCEL_UPLOAD_UNCONFIRMED: 'Результат записи пока не подтверждён. Перед повтором сервис сверит файл.',
};

export function excelDeliveryView(state: ExcelState | null, version: number, enabled: boolean, unavailable: boolean) {
  if (!enabled) return { tone: 'is-disabled', sent: false, message: 'Автозаполнение Excel не подключено. Сохранённые ответы доступны в приложении.' };
  if (unavailable) return { tone: 'is-warning', sent: false, message: 'Не удалось проверить отправку в Excel. Обновите данные или войдите заново.' };
  if (!state) return { tone: 'is-pending', sent: false, message: 'Проверяем отправку в Excel…' };
  if (state.last_error_code) return { tone: 'is-warning', sent: false, message: messages[state.last_error_code] ?? 'Обновление Excel ожидает повторной отправки. Данные сохранены в приложении.' };
  if (state.last_success_at && Number.isFinite(Date.parse(state.last_success_at)) && state.synced_version >= Math.max(state.requested_version, version)) {
    return { tone: 'is-synced', sent: true, message: 'Сохранённые ответы отправлены в Excel.' };
  }
  return { tone: 'is-pending', sent: false, message: 'Последние сохранённые изменения ожидают отправки в Excel.' };
}

export function excelSuccessTime(value: string | null, timezone: string): string | null {
  if (!value || !Number.isFinite(Date.parse(value)) || !timezone) return null;
  try {
    return new Intl.DateTimeFormat('ru-RU', { timeZone: timezone, dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value));
  } catch { return null; }
}

export function safeExcelURL(raw?: string): string | null {
  if (!raw || raw.trim() !== raw) return null;
  // The API constructs this canonical fixed-host URL; never render arbitrary schemes.
  return /^https:\/\/drive\.google\.com\/file\/d\/[A-Za-z0-9_-]{10,200}\/view$/.test(raw) ? raw : null;
}
