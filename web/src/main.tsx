import { StrictMode, useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import type { FormEvent } from 'react';
import { api, APIError } from './api';
import type { Identity, User } from './api';
import './style.css';
import { ChecklistPanel } from './ChecklistPanel';
import { HistoryPanel } from './HistoryPanel';
import { ExportPanel } from './ExportPanel';
import { EmployeePanel } from './EmployeePanel';
import { AuditPanel } from './AuditPanel';
import { ManagerDashboard } from './ManagerDashboard';
import { confirmChecklistDiscard, hasUnsavedChecklist } from './checklist-drafts';
import { ThemeToggle } from './ThemeToggle';

function App() {
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [options, setOptions] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [environment, setEnvironment] = useState<string | null>(null);
  const [excelSyncEnabled,setExcelSyncEnabled]=useState(false);
 const [clubTimezone,setClubTimezone]=useState('UTC');
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [messageKind, setMessageKind] = useState<'error' | 'success'>('error');
  const [mode, setMode] = useState<'admin' | 'manager'>('admin');
  const [users, setUsers] = useState<User[]>([]);
 const [newRole,setNewRole]=useState('admin');
  const [enrollment, setEnrollment] = useState('');
  const [devices, setDevices] = useState<{ id: string; label: string; expires_at: string; revoked: boolean }[]>([]);

  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (!hasUnsavedChecklist()) return;
      event.preventDefault(); event.returnValue = '';
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, []);

  async function refresh() {
    const status = await api<{ environment: string; excel_sync_enabled: boolean;timezone:string }>('/status');
    setEnvironment(status.environment);
 setClubTimezone(status.timezone);
    setExcelSyncEnabled(status.excel_sync_enabled);
    let me: Identity | null = null;
    try { me = await api<Identity>('/auth/me'); }
    catch (error) { if (!(error instanceof APIError) || !['AUTH_REQUIRED', 'DEVICE_REQUIRED'].includes(error.code)) throw error; }
    setIdentity(me);
    try { setOptions(await api<User[]>('/auth/admin-options')); }
    catch (error) { if (!(error instanceof APIError) || error.code !== 'DEVICE_REQUIRED') throw error; setOptions([]); }
    if (me?.user.roles.includes('manager')) {
      setUsers(await api<User[]>('/users'));
      setDevices(await api<typeof devices>('/devices'));
    } else { setUsers([]); setDevices([]); }
    return me;
  }
  useEffect(() => {
    void refresh().catch(() => setMessage('Не удалось связаться с сервером. Обновите страницу.')).finally(() => setLoading(false));
  }, []);
  useEffect(() => {
    if (!identity) return;
    const actor = identity.user.id;
    let lastActivity = 0;
    const activity = () => {
      if (Date.now() - lastActivity < 60_000) return;
      lastActivity = Date.now();
      void api('/auth/activity', 'POST', {}, actor).catch(error => {
        if (error instanceof APIError && ['AUTH_REQUIRED', 'SESSION_CHANGED', 'DEVICE_REQUIRED'].includes(error.code)) {
          setIdentity(null); setMessageKind('error'); setMessage('Сессия завершена или пользователь изменён. Войдите заново.');
        }
      });
    };
    window.addEventListener('pointerdown', activity);
    window.addEventListener('keydown', activity);
    return () => { window.removeEventListener('pointerdown', activity); window.removeEventListener('keydown', activity); };
  }, [identity]);

  async function perform(work: () => Promise<void>) {
    setBusy(true); setMessage(''); setMessageKind('success');
    try { await work(); await refresh(); }
    catch (error) {
      setMessageKind('error');
      setMessage(error instanceof APIError ? error.message : 'Не удалось связаться с сервером. Действие могло сохраниться — обновите страницу перед повтором.');
      if (error instanceof APIError && ['AUTH_REQUIRED', 'SESSION_CHANGED', 'DEVICE_REQUIRED'].includes(error.code)) setIdentity(null);
    } finally { setBusy(false); }
  }
  function submit(event: FormEvent<HTMLFormElement>, work: (data: FormData) => Promise<void>) {
    event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
    // Keep names and selections on failure; clear submitted secrets immediately.
    form.querySelectorAll<HTMLInputElement>('input[type="password"]').forEach(input => { input.value = ''; });
    void perform(async () => { await work(data); form.reset(); });
  }
  const text = (data: FormData, key: string) => String(data.get(key) ?? '');
  const actor = identity?.user.id ?? '';

  return <main>
    <a className="skip-link" href="#workspace">К рабочему экрану</a>
    <header className="app-header"><a className="brand" href="#workspace"><span className="brand-mark" aria-hidden="true">PC</span><span>CLUB ADMIN<small>Управление сменами</small></span></a><div className="header-actions"><span className="mode-badge">{environment === 'test' ? 'Тестовый режим' : environment === 'production' ? 'Рабочие операции заблокированы' : 'Подключение…'}</span><ThemeToggle /></div></header>
    <div className="page-heading" id="workspace" tabIndex={-1}><p className="eyebrow">{identity ? 'Рабочее пространство' : 'Компьютерный клуб'}</p>
    <h1>{identity ? identity.user.display_name : 'Вход в приложение'}</h1><p className="page-description">{identity ? 'Смена, проверки и передача ответственности — в одном месте.' : 'Войдите в личную учётную запись, чтобы продолжить работу.'}</p></div>
    <aside className="notice mode-notice"><strong>{environment === 'production' ? 'Рабочие операции заблокированы' : 'Демонстрационная версия'}</strong><p>Кассовая политика не подтверждена. Используйте только вымышленные тестовые данные.</p><details><summary>Правила работы в тестовом режиме</summary><p>Приём и передача требуют чек-листа, пересчёта кассы и личного PIN. Передача выполняется двумя сотрудниками на одном зарегистрированном браузере. Production заблокирован до проверки денежной политики.</p></details></aside>
    {message && <p className={messageKind} role={messageKind === 'error' ? 'alert' : 'status'}>{message}</p>}
    {loading ? <p role="status">Проверяем сессию…</p> : identity ? <>
      <section className="identity-card"><h2>Личный вход подтверждён</h2><p>Роли: {identity.user.roles.map(role => role === 'manager' ? 'управляющий' : 'администратор').join(', ')}.</p>
        <p>{identity.device_trusted ? 'Браузер зарегистрирован в клубе.' : 'Удалённый вход. Доверие клубному браузеру не назначено.'}</p>
        <button disabled={busy} onClick={() => { if (!confirmChecklistDiscard()) return; void perform(async () => { await api('/auth/logout', 'POST', {}, actor); setIdentity(null); setEnrollment(''); }); }}>Выйти / сменить сотрудника</button>
      </section>
      <nav className="workspace-nav" aria-label="Разделы приложения"><a href="#current-shift">Текущая смена</a><a href="#shift-history">История</a>{identity.user.roles.includes('manager') && <><a href="#club-overview">Обзор клуба</a><a href="#reports">Отчёты и аудит</a><a href="#team">Сотрудники и доступ</a></>}</nav>
      <div id="current-shift"><ChecklistPanel key={identity.user.id} identity={identity} users={identity.user.roles.includes('manager')?users:options} excelSyncEnabled={excelSyncEnabled} onSessionEnd={refresh} /></div>
      {identity.user.roles.includes('manager')&&<div id="club-overview"><ManagerDashboard key={'dashboard:'+identity.user.id}/></div>}
      <div id="shift-history"><HistoryPanel key={'history:'+identity.user.id} identity={identity} users={identity.user.roles.includes('manager')?users:options} timezone={clubTimezone}/></div>
      {identity.user.roles.includes('manager')&&<div id="reports"><ExportPanel key={'exports:'+identity.user.id} users={users}/><AuditPanel key={'audit:'+identity.user.id} users={users} timezone={clubTimezone}/></div>}
      {identity.user.roles.includes('manager') && <>
        <section id="team"><h2>Сотрудники</h2><ul>{users.map(user => <li key={user.id}>{user.display_name} · {user.roles.map(role => role === 'manager' ? 'управляющий' : 'администратор').join(', ')} · {user.active ? 'активен' : 'отключён'}</li>)}</ul>
          <form onSubmit={event => submit(event, async data => {
            const role=text(data,'role');
            await api('/users', 'POST', { display_name: text(data, 'name'), roles: role==='both'?['admin','manager']:[role], ...(role!=='manager'?{pin:text(data,'pin')}:{}), ...(role!=='admin'?{manager_login:text(data,'login'),password:text(data,'password')}:{}) }, actor);
            setMessage('Сотрудник создан. Учётные данные не выводятся и не сохраняются в браузере.');
          })}>
            <h3>Добавить сотрудника</h3><label>Роль нового сотрудника<select name="role" aria-label="Роль нового сотрудника" value={newRole} onChange={event=>setNewRole(event.target.value)}><option value="admin">Администратор</option><option value="manager">Управляющий</option><option value="both">Администратор и управляющий</option></select></label><label>Имя<input name="name" required maxLength={100} autoComplete="off" /></label>
            <>{newRole!=='manager'&&<label>Личный PIN · 6 цифр<input name="pin" type="password" inputMode="numeric" pattern="[0-9]{6}" minLength={6} maxLength={6} required autoComplete="new-password" /></label>}{newRole!=='admin'&&<><label>Логин нового управляющего<input name="login" required minLength={3} maxLength={100} autoComplete="off"/></label><label>Пароль нового управляющего<input name="password" type="password" required minLength={12} maxLength={256} autoComplete="new-password"/></label></>}</>
            <button disabled={busy}>Создать сотрудника</button>
          </form>
        </section>
        <EmployeePanel key={identity.user.id} users={users} actor={actor} refresh={async () => { await refresh(); }}/>
        <section><h2>Доверенные браузеры</h2><ul>{devices.map(device => <li key={device.id}>{device.label} · {device.revoked ? 'доступ отозван' : `действует до ${new Date(device.expires_at).toLocaleDateString('ru-RU')}`} {!device.revoked && <button disabled={busy} onClick={() => void perform(async () => { await api('/devices/' + device.id + '/revoke', 'POST', {}, actor); })}>Отозвать доступ</button>}</li>)}</ul>
          <form onSubmit={event => submit(event, async data => { const result = await api<{ code: string }>('/devices/enrollment', 'POST', { password: text(data, 'password') }, actor); setEnrollment(result.code); })}>
            <h3>Выдать одноразовый код регистрации</h3><label>Повторите пароль управляющего<input name="password" type="password" maxLength={256} required autoComplete="current-password" /></label><button disabled={busy}>Создать код на 10 минут</button>
          </form>
          {enrollment && <p>Введите этот код в клубном браузере. Он используется один раз: <code className="code">{enrollment}</code><button onClick={() => setEnrollment('')}>Скрыть код</button></p>}
        </section>
      </>}
    </> : <section className="login-card">
      <nav aria-label="Способ входа"><button disabled={busy} aria-pressed={mode === 'admin'} onClick={() => setMode('admin')}>Администратор · PIN</button><button disabled={busy} aria-pressed={mode === 'manager'} onClick={() => setMode('manager')}>Управляющий · пароль</button></nav>
      {mode === 'admin' ? options.length ? <form onSubmit={event => submit(event, async data => { await api('/auth/pin-login', 'POST', { user_id: text(data, 'user'), pin: text(data, 'pin') }); })}>
        <label>Сотрудник<select name="user" required defaultValue=""><option value="" disabled>Выберите своё имя</option>{options.map(user => <option key={user.id} value={user.id}>{user.display_name}</option>)}</select></label>
        <label>Личный PIN<input name="pin" type="password" inputMode="numeric" pattern="[0-9]{6}" maxLength={6} required autoComplete="off" /></label><button disabled={busy}>Войти по PIN</button>
      </form> : <p>Для входа администраторов зарегистрируйте этот браузер кодом от управляющего. Если сотрудников ещё нет, создайте их под управляющим.</p> : <form onSubmit={event => submit(event, async data => { await api('/auth/manager-login', 'POST', { login: text(data, 'login'), password: text(data, 'password') }); })}>
        <label>Логин управляющего<input name="login" required maxLength={100} autoComplete="username" /></label><label>Пароль<input name="password" type="password" required maxLength={256} autoComplete="current-password" /></label><button disabled={busy}>Войти как управляющий</button>
      </form>}
      <details><summary>Зарегистрировать браузер клуба</summary><form onSubmit={event => submit(event, async data => { await api('/devices/register', 'POST', { code: text(data, 'code'), label: text(data, 'label') }); setMessage('Браузер зарегистрирован. Войдите по личному PIN.'); })}>
        <label>Одноразовый код<input name="code" required minLength={64} maxLength={64} autoComplete="off" /></label><label>Название браузера<input name="label" required maxLength={100} /></label><button disabled={busy}>Зарегистрировать</button>
      </form><p>Это доверие браузеру, а не физическая привязка к компьютеру. После очистки cookie потребуется новый код.</p></details>
    </section>}
    <footer>Дневная смена 09:00–21:00 · Ночная 21:00–09:00. Ответственность передаётся только после персонального приёма.</footer>
  </main>;
}
const root = document.getElementById('root');
if (!root) throw new Error('Root element missing');
createRoot(root).render(<StrictMode><App /></StrictMode>);
