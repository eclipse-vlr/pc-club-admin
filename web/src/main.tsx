import { StrictMode, useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import './style.css';

type Status = { name: string; stage: string; environment: 'test' | 'production'; timezone: string; cash_policy_status: string; operations_enabled: boolean };
function App() {
  const [status, setStatus] = useState<Status | null>(null);
  const [error, setError] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    void fetch('/api/v1/status', { signal: controller.signal }).then(async response => {
      if (!response.ok) throw new Error('API unavailable');
      const result = await response.json() as Status;
      if (result.stage !== 'foundation' || typeof result.timezone !== 'string') throw new Error('Invalid status');
      setStatus(result);
    }).catch(() => { if (!controller.signal.aborted) setError(true); });
    return () => controller.abort();
  }, []);
  return <main>
    <header><p className="brand">PC CLUB ADMIN</p><span>Этапы 0–1 · Каркас</span></header>
    <h1>Рабочее место администратора</h1>
    <section className="notice"><h2>Работа со сменами пока недоступна</h2><p>Вход сотрудников и передача смен появятся на следующих этапах. Реальные кассовые данные здесь не вводятся.</p></section>
    <section aria-live="polite"><h2>Состояние приложения</h2>
      {error ? <p role="alert">Не удалось связаться с сервером. Обновите страницу после восстановления связи.</p> : status ? <dl>
        <dt>API</dt><dd>Доступен</dd><dt>Режим</dt><dd>{status.environment === 'test' ? 'Тестовый' : 'Production: рабочие операции заблокированы'}</dd>
        <dt>Часовой пояс клуба</dt><dd>{status.timezone}</dd><dt>Кассовая политика</dt><dd>Не подтверждена. Тестовый расчёт кассы; production запрещён до проверки.</dd>
      </dl> : <p>Проверяем связь с сервером…</p>}
    </section>
    <footer>Дневная смена 09:00–21:00 · Ночная 21:00–09:00. Ответственность передаётся только после персонального приёма.</footer>
  </main>;
}
const root = document.getElementById('root');
if (!root) throw new Error('Root element missing');
createRoot(root).render(<StrictMode><App /></StrictMode>);
