/* global window, Event */
// Chromium with synthetic, intercepted API responses only; no real accounts or credentials.
import assert from 'node:assert/strict';
import { after, test } from 'node:test';
import { readFile } from 'node:fs/promises';
import { fileURLToPath, URL } from 'node:url';
import path from 'node:path';
import process from 'node:process';
import { chromium } from 'playwright-core';

const origin = 'http://127.0.0.1:54321';
const dist = fileURLToPath(new URL('../dist/', import.meta.url));
const manager = { id: '11111111-1111-4111-8111-111111111111', display_name: 'Управляющий теста', manager_login: 'manager', roles: ['manager'], active: true, version: 1 };
const employee = { ...manager, id: '22222222-2222-4222-8222-222222222222', display_name: 'Администратор теста', manager_login: '', roles: ['admin'] };
const browser = await chromium.launch({ executablePath: process.env.PC_CLUB_CHROMIUM || '/usr/bin/chromium', args: ['--no-sandbox'] });
after(() => browser.close());

async function fixture(t, signedIn = true) {
  const context = await browser.newContext();
  const errors = [];
  let release;
  let reached;
  const gate = new Promise(resolve => { release = resolve; });
  const started = new Promise(resolve => { reached = resolve; });
  const state = { signedIn, users: [manager, employee], mutations: [], hold: false, reject: false, failUsers: false, loseResponse: false };
  t.after(async () => { release(); await context.close(); assert.deepEqual(errors, []); });
  await context.addInitScript(() => {
    window.csrfCalls = 0;
    const fetch = window.fetch;
    window.fetch = (...args) => {
      if (args[0] === '/api/v1/auth/csrf') window.csrfCalls++;
      return fetch(...args);
    };
  });
  await context.route('**/*', async route => {
    const request = route.request();
    const url = new URL(request.url());
    assert.equal(url.origin, origin, 'no external requests');
    const endpoint = url.pathname.slice('/api/v1'.length);
    if (url.pathname.startsWith('/api/')) {
      if (request.method() !== 'GET') {
        if (endpoint === '/auth/activity') return route.fulfill({ status: 204 });
        assert.ok(['/auth/manager-login', '/users', '/users/' + employee.id, '/users/' + employee.id + '/reset-pin'].includes(endpoint), endpoint);
        state.mutations.push({ endpoint, body: request.postDataJSON() });
        reached();
        if (state.hold) await gate;
        if (state.reject) return route.fulfill({ status: 409, json: { error: { code: 'VERSION_CONFLICT', message: 'Данные сотрудника изменились.' } } });
        if (endpoint === '/auth/manager-login') state.signedIn = true;
        if (endpoint === '/users') state.users.push({ ...employee, ...request.postDataJSON(), id: 'new-user', version: 1 });
        if (endpoint === '/users/' + employee.id + '/reset-pin') state.users = state.users.map(user => user.id === employee.id ? { ...user, version: user.version + 1 } : user);
        if (state.loseResponse) return route.abort('connectionreset');
        return route.fulfill({ json: {} });
      }
      if (endpoint === '/auth/me') return state.signedIn
        ? route.fulfill({ json: { user: manager, device_trusted: true, pin_session: false } })
        : route.fulfill({ status: 401, json: { error: { code: 'AUTH_REQUIRED' } } });
      if (endpoint === '/users' && state.failUsers) return route.fulfill({ status: 503, json: { error: { code: 'SERVICE_UNAVAILABLE' } } });
      const fixtures = {
        '/status': { environment: 'test', excel_sync_enabled: false, timezone: 'Europe/Moscow' },
        '/auth/admin-options': [employee], '/auth/csrf': { csrf_token: 'synthetic-csrf' },
        '/users': state.users, '/devices': [], '/shifts/current': { shift: null },
        '/manager/dashboard': { observed_at: '2026-10-23T06:00:00Z', timezone: 'Europe/Moscow', state: 'uninitialized', responsible: null, initial_opening: null, pending_handover: null },
      };
      assert.ok(Object.hasOwn(fixtures, endpoint), endpoint);
      return route.fulfill({ json: fixtures[endpoint] });
    }
    assert.ok(url.pathname === '/' || /^\/assets\/[A-Za-z0-9._-]+$/.test(url.pathname));
    const name = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
    return route.fulfill({ body: await readFile(path.join(dist, name)), contentType: name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' });
  });
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(origin);
  await page.getByRole('button', { name: state.signedIn ? 'Создать сотрудника' : 'Управляющий · пароль', exact: true }).waitFor();
  return { page, state, started, release };
}

async function doubleSubmit(form) {
  await form.evaluate(element => {
    window.csrfCalls = 0;
    element.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    element.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  });
}

test('manager login ignores a second submit before React disables the button', async t => {
  const { page, state, started, release } = await fixture(t, false);
  await page.getByRole('button', { name: 'Управляющий · пароль', exact: true }).click();
  await page.getByLabel('Логин управляющего', { exact: true }).fill('manager');
  await page.getByLabel('Пароль', { exact: true }).fill('synthetic-password');
  state.hold = true;
  await doubleSubmit(page.locator('form').filter({ has: page.getByRole('button', { name: 'Войти как управляющий', exact: true }) }));
  await started;
  assert.equal(await page.evaluate(() => window.csrfCalls), 1, 'only one login attempt starts');
  assert.equal(await page.getByLabel('Пароль', { exact: true }).inputValue(), '');
  release();
  await page.getByRole('heading', { level: 1, name: manager.display_name }).waitFor();
  assert.equal(state.mutations.length, 1, 'one session is created');
});

test('reset PIN ignores a second submit and clears the submitted PIN', async t => {
  const { page, state, started, release } = await fixture(t);
  const panel = page.getByRole('region', { name: 'Управление сотрудниками' });
  await panel.getByText('Изменить: ' + employee.display_name, { exact: true }).click();
  await panel.getByLabel('Новый личный PIN', { exact: true }).fill('123456');
  state.hold = true;
  await doubleSubmit(panel.locator('form').filter({ has: page.getByRole('button', { name: 'Сбросить PIN', exact: true }) }));
  await started;
  assert.equal(await page.evaluate(() => window.csrfCalls), 1, 'only one PIN reset starts');
  assert.equal(await panel.getByLabel('Новый личный PIN', { exact: true }).inputValue(), '');
  release();
  await panel.getByRole('status').filter({ hasText: 'PIN изменён' }).waitFor();
  assert.equal(state.mutations.length, 1);
  assert.equal(state.mutations[0].body.pin, '123456');
});

test('creating an employee ignores a second submit before React renders', async t => {
  const { page, state, started, release } = await fixture(t);
  await page.getByLabel('Имя', { exact: true }).fill('Новый администратор');
  await page.getByLabel('Личный PIN · 6 цифр', { exact: true }).fill('123456');
  state.hold = true;
  await doubleSubmit(page.locator('form').filter({ has: page.getByRole('button', { name: 'Создать сотрудника', exact: true }) }));
  await started;
  assert.equal(await page.evaluate(() => window.csrfCalls), 1, 'only one employee creation starts');
  release();
  await page.getByRole('status').filter({ hasText: 'Сотрудник создан' }).waitFor();
  assert.equal(state.mutations.length, 1, 'only one employee is created');
  assert.equal(state.mutations[0].body.pin, '123456');
});

test('a lost PIN reset response is recovered by reading without another reset', async t => {
  const { page, state } = await fixture(t);
  const panel = page.getByRole('region', { name: 'Управление сотрудниками' });
  await panel.getByText('Изменить: ' + employee.display_name, { exact: true }).click();
  await panel.getByLabel('Новый личный PIN', { exact: true }).fill('123456');
  state.loseResponse = true;
  await panel.getByRole('button', { name: 'Сбросить PIN', exact: true }).click();
  await panel.getByRole('alert').filter({ hasText: 'Связь с сервером прервалась' }).waitFor();
  assert.equal(await panel.getByLabel('Новый личный PIN', { exact: true }).inputValue(), '');
  assert.ok(await panel.getByRole('button', { name: 'Сбросить PIN', exact: true }).isDisabled());
  await panel.getByRole('button', { name: 'Перечитать сотрудников', exact: true }).click();
  await panel.getByText('Изменить: ' + employee.display_name, { exact: true }).click();
  assert.ok(await panel.getByRole('button', { name: 'Сбросить PIN', exact: true }).isEnabled());
  assert.equal(state.mutations.length, 1, 'recovery never repeats a committed PIN reset');
  assert.equal(state.users.find(user => user.id === employee.id).version, 2);
});

test('employee mutations stay blocked after a failure until a successful reload', async t => {
  const { page, state } = await fixture(t);
  const panel = page.getByRole('region', { name: 'Управление сотрудниками' });
  await panel.getByText('Изменить: ' + employee.display_name, { exact: true }).click();
  const form = panel.locator('form').filter({ has: page.getByRole('button', { name: 'Сохранить сотрудника', exact: true }) }).last();
  state.reject = true;
  await form.getByRole('button').click();
  await panel.getByRole('alert').waitFor();
  assert.ok(await form.getByRole('button').isDisabled());
  await form.evaluate(element => {
    window.csrfCalls = 0;
    element.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  });
  assert.equal(await page.evaluate(() => window.csrfCalls), 0, 'a stale form cannot start another mutation');
  assert.equal(state.mutations.length, 1);
  state.failUsers = true;
  await panel.getByRole('button', { name: 'Перечитать сотрудников', exact: true }).click();
  await panel.getByRole('alert').filter({ hasText: 'Не удалось перечитать сотрудников' }).waitFor();
  assert.ok(await form.getByRole('button').isDisabled(), 'a failed reload never unlocks changes');
  state.reject = false;
  state.failUsers = false;
  await panel.getByRole('button', { name: 'Перечитать сотрудников', exact: true }).click();
  await panel.getByText('Изменить: ' + employee.display_name, { exact: true }).click();
  await panel.getByLabel('Имя сотрудника', { exact: true }).last().fill('Новое имя');
  await panel.getByRole('button', { name: 'Сохранить сотрудника', exact: true }).last().click();
  await panel.getByRole('status').filter({ hasText: 'Данные сотрудника сохранены' }).waitFor();
  assert.equal(state.mutations.length, 2, 'reload enables a deliberate new attempt');
});
