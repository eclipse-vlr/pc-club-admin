import { spawn, execFileSync } from 'node:child_process';
import { mkdir, open, readFile, unlink } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createStagingEnv } from './init-staging.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const directory = path.join(root, 'deploy/.portfolio-demo');
const envFile = path.join(directory, '.env');
export const project = 'pc-club-portfolio-demo';
export const origin = 'http://127.0.0.1:18081';
export const demo = {
  login: 'demo-manager', password: 'portfolio-demo-only-2026',
  manager: 'Управляющий демоклуба «Орион»',
  employees: [
    { name: 'Алексей Демо', pin: '123456' },
    { name: 'Мария Демо', pin: '654321' },
  ],
};

export function parseArguments(args) {
  const [command, ...rest] = args;
  if (!['up', 'stop', 'reset', 'status'].includes(command)) throw new Error('Команда: up, stop, status или reset --environment portfolio-demo');
  if (command === 'reset') {
    if (rest.length !== 2 || rest[0] !== '--environment' || rest[1] !== 'portfolio-demo') {
      throw new Error('Сброс требует явного выбора: reset --environment portfolio-demo');
    }
  } else if (rest.length) throw new Error('Неизвестные аргументы');
  return command;
}

export function validateEnvironment(text) {
  const values = {};
  for (const line of text.split(/\r?\n/).filter(line => line && !line.startsWith('#'))) {
    const match = /^([A-Z_]+)=(.*)$/.exec(line);
    if (!match || Object.hasOwn(values, match[1])) throw new Error('Некорректная конфигурация демо');
    values[match[1]] = match[2];
  }
  const expected = {
    DEMO_ENVIRONMENT: 'portfolio-demo', STAGING_IMAGE: 'pc-club-admin:portfolio-demo',
    STAGING_TIMEZONE: 'Europe/Moscow', STAGING_HTTP_PORT: '18081',
  };
  if (Object.keys(values).length !== 6 || Object.entries(expected).some(([key, value]) => values[key] !== value)
      || !/^[a-f0-9]{64}$/.test(values.STAGING_DB_PASSWORD)
      || !/^[a-f0-9]{64}$/.test(values.STAGING_PIN_PEPPER)) throw new Error('Конфигурация не принадлежит portfolio-demo');
  return values;
}

// Never inherit another installation's Compose substitutions.
const environment = Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^(STAGING_|COMPOSE_)/i.test(key)));
function run(args, input, inherit = false) {
  return new Promise((resolve, reject) => {
    const child = spawn('docker', args, { cwd: root, env: environment, stdio: ['pipe', inherit ? 'inherit' : 'pipe', inherit ? 'inherit' : 'pipe'] });
    let output = '';
    child.stdout?.on('data', chunk => output += chunk);
    // Docker errors can contain expanded environment variables. Do not relay them.
    child.stderr?.resume();
    child.once('error', () => reject(new Error('Не удалось запустить Docker')));
    child.once('close', code => code === 0 ? resolve(output.trim()) : reject(new Error('Команда Docker не выполнена (код ' + code + ')')));
    child.stdin.on('error', () => {});
    child.stdin.end(input);
  });
}
const compose = (args, input) => run(['compose', '--env-file', envFile, '-f', 'deploy/compose.staging.yaml', '-f', 'deploy/compose.portfolio.yaml', '-p', project, ...args], input);

export function validateCompose(config) {
  const api = config.services.api;
  const db = config.services.db;
  if (config.name !== project || api.environment.APP_ENV !== 'test'
      || api.environment.CASH_POLICY_STATUS !== 'unverified' || api.environment.DRIVE_EXCEL_SYNC !== 'disabled'
      || api.environment.APP_ORIGIN !== origin || api.image !== 'pc-club-admin:portfolio-demo'
      || api.ports.length !== 1 || api.ports[0].host_ip !== '127.0.0.1' || String(api.ports[0].published) !== '18081'
      || db.environment.POSTGRES_DB !== 'pcclub_staging' || db.ports?.length
      || config.volumes.staging_postgres_data.name !== project + '_staging_postgres_data'
      || Object.keys(config.volumes).length !== 1
      || Object.values(config.services).some(service => service.volumes?.some(volume => volume.type === 'bind')))
    throw new Error('Небезопасная конфигурация демо');
}

export function validateVolume(volume) {
  if (volume.Name !== project + '_staging_postgres_data'
      || volume.Labels?.['com.docker.compose.project'] !== project
      || volume.Labels?.['com.docker.compose.volume'] !== 'staging_postgres_data') throw new Error('Том не принадлежит portfolio-demo; сброс запрещён');
}

export function apiClient(base = origin) {
  if (base !== origin) throw new Error('Разрешён только фиксированный loopback-адрес демо');
  const cookies = new Map();
  let actor = '';
  async function request(route, method = 'GET', body, key) {
    const headers = { Cookie: [...cookies].map(([name, value]) => name + '=' + value).join('; ') };
    if (method !== 'GET') {
      const csrf = await request('/auth/csrf');
      headers.Cookie = [...cookies].map(([name, value]) => name + '=' + value).join('; ');
      Object.assign(headers, { Origin: base, 'Content-Type': 'application/json', 'X-CSRF-Token': csrf.csrf_token, 'X-Actor-ID': actor });
      if (key) headers['Idempotency-Key'] = key;
    }
    const response = await fetch(base + '/api/v1' + route, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), redirect: 'error', signal: AbortSignal.timeout(20000) });
    for (const cookie of response.headers.getSetCookie()) {
      const [name, ...parts] = cookie.split(';')[0].split('=');
      cookies.set(name, parts.join('='));
    }
    const result = response.status === 204 ? null : await response.json();
    if (!response.ok) throw new Error('Демо API: ' + (result?.error?.code ?? response.status));
    if (result?.identity) actor = result.identity.user.id;
    return result;
  }
  return request;
}

export async function seedDemo(api) {
  const status = await api('/status');
  if (status.environment !== 'test' || status.cash_policy_status !== 'unverified' || status.excel_sync_enabled) throw new Error('Сервер не является изолированным демо');
  await api('/auth/manager-login', 'POST', { login: demo.login, password: demo.password });
  try {
    const users = await api('/users');
    const employees = [];
    for (const employee of demo.employees) {
      const matches = users.filter(user => user.display_name === employee.name);
      if (matches.length > 1) throw new Error('Дубли сотрудников демо');
      const user = matches[0] ?? await api('/users', 'POST', { display_name: employee.name, roles: ['admin'], pin: employee.pin });
      if (!user.active || user.roles.length !== 1 || user.roles[0] !== 'admin') throw new Error('Доступ сотрудника демо изменён; требуется явный сброс');
      employees.push(user);
    }
    const current = await api('/shifts/current');
    if (!current.shift) {
      await api('/shifts/bootstrap', 'POST', {
        owner_id: employees[0].id, business_date: '2026-11-02', kind: 'day',
        opening_cash_kopecks: 1000000, reason: 'Вымышленный начальный остаток демоклуба «Орион»',
      }, 'portfolio-demo-first-shift-v1');
    }
  } finally { await api('/auth/logout', 'POST', {}); }
}

async function main(args) {
  const command = parseArguments(args);
  await mkdir(directory, { recursive: true });
  const lockPath = path.join(directory, 'operation.lock');
  const lock = await open(lockPath, 'wx', 0o600);
  try {
    try { validateEnvironment(await readFile(envFile, 'utf8')); }
    catch (error) {
      if (error.code !== 'ENOENT' || command !== 'up') throw error;
      const revision = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
      // Reuse the exclusive secret generator, then adapt the newly created file only.
      const temporary = path.join(directory, 'generated.env');
      await createStagingEnv(temporary, revision);
      const source = await readFile(temporary, 'utf8');
      const handle = await open(envFile, 'wx', 0o600);
      try {
        await handle.writeFile(source.replace('pc-club-admin:' + revision, 'pc-club-admin:portfolio-demo').replace('STAGING_HTTP_PORT=18080', 'STAGING_HTTP_PORT=18081') + 'DEMO_ENVIRONMENT=portfolio-demo\n');
      } finally { await handle.close(); }
      await unlink(temporary);
    }
    validateEnvironment(await readFile(envFile, 'utf8'));
    validateCompose(JSON.parse(await compose(['--profile', 'tools', 'config', '--format', 'json'])));
    if (command === 'status') { console.log(await compose(['ps'])); return; }
    if (command === 'stop') { await compose(['stop']); console.log('Демо остановлено. Данные сохранены.'); return; }
    if (command === 'reset') {
      const names = (await run(['volume', 'ls', '--format', '{{.Name}}'])).split('\n');
      const name = project + '_staging_postgres_data';
      if (names.includes(name)) validateVolume(JSON.parse(await run(['volume', 'inspect', name]))[0]);
      await compose(['down', '--volumes', '--remove-orphans']);
      console.log('Удалены только контейнеры и том portfolio-demo. Для начала сценария: node scripts/portfolio-demo.mjs up');
      return;
    }
    const revision = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
    const dirty = execFileSync('git', ['status', '--porcelain'], { cwd: root, encoding: 'utf8' }).trim().length > 0;
    console.log('Сборка демо: ' + revision + (dirty ? ' + локальные изменения' : ''));
    await run(['build', '--build-arg', 'SOURCE_REVISION=' + revision + (dirty ? '-dirty' : ''), '-t', 'pc-club-admin:portfolio-demo', '.'], undefined, true);
    await compose(['up', '-d', '--wait', '--wait-timeout', '90', 'db']);
    await compose(['run', '--rm', '-T', 'migrate']);
    const count = await compose(['exec', '-T', 'db', 'psql', '-U', 'pcclub_staging', '-d', 'pcclub_staging', '-At', '-v', 'ON_ERROR_STOP=1', '-c', 'SELECT count(*) FROM users']);
    if (count === '0') await compose(['run', '--rm', '-T', 'admin', 'bootstrap-manager', '--login', demo.login, '--name', demo.manager, '--password-stdin'], demo.password + '\n');
    await compose(['up', '-d', '--wait', '--wait-timeout', '90', 'api']);
    await seedDemo(apiClient());
    console.log('Демонстрация. Вымышленные данные: ' + origin);
    console.log('Учётные данные и сценарий: docs/demo.md. Повторный up сохраняет текущую смену.');
  } finally { await lock.close(); await unlink(lockPath); }
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main(process.argv.slice(2)).catch(error => { console.error(error.code === 'EEXIST' ? 'Операция уже выполняется или конфигурация существует. Не удаляйте исходные секреты.' : error.message); process.exitCode = 1; });
}
