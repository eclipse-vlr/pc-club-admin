/* global document, window, Event */
// Only intercepted fixtures: no API process, database or external services.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { fileURLToPath, URL } from 'node:url';
import { log } from 'node:console';
import path from 'node:path';
import process from 'node:process';
import { chromium } from 'playwright-core';

const origin = 'http://127.0.0.1:54321';
const dist = fileURLToPath(new URL('../dist/', import.meta.url));
const user = { id: '11111111-1111-4111-8111-111111111111', display_name: 'Тестовый администратор', roles: ['admin'], active: true };
let shift = { id: '22222222-2222-4222-8222-222222222222', owner_id: user.id, status: 'opening', kind: 'day', business_date: '2026-10-20', template_version: 1, version: 1, notes: [], cash: null, handover_id: '', handover_parent_id: '', checklist: ['accept_d', 'accept_e'].map((code, i) => ({ code, phase: 'acceptance', label: 'Пункт ' + (i + 1), help_text: '', answer: 'unanswered', comment: '', equipment_check: false, equipment_issue: false, answered_by: null, version: 1 })) };
let concurrent = false;
let savedFirst = false;
let logoutCalls = 0;
let lastVersion = null;
const unexpected = [];
const browser = await chromium.launch({ executablePath: process.env.PC_CLUB_CHROMIUM || '/usr/bin/chromium', headless: true, args: ['--no-sandbox'] });
try {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
  await context.route('**/*', async route => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== origin) { unexpected.push('external request'); await route.abort(); return; }
    if (url.pathname.startsWith('/api/')) {
      if (request.method() === 'POST' && url.pathname === '/api/v1/auth/activity') { await route.fulfill({ status: 204 }); return; }
      if (request.method() === 'POST' && url.pathname === '/api/v1/auth/logout') { logoutCalls++; await route.fulfill({ status: 204 }); return; }
      if (request.method() === 'PATCH' && url.pathname.startsWith('/api/v1/shifts/' + shift.id + '/checklist/')) {
        const body = request.postDataJSON();
        const code = url.pathname.split('/').at(-1);
        const entry = shift.checklist.find(item => item.code === code);
        lastVersion = body.version;
        if (body.version !== entry.version) { await route.fulfill({ status: 409, json: { error: { code: 'VERSION_CONFLICT', message: 'Данные изменены. Обновите страницу' } } }); return; }
        shift = { ...shift, version: shift.version + 1, checklist: shift.checklist.map(item => item.code === code ? { ...item, ...body, answered_by: user.id, version: item.version + 1 } : item) };
        if (concurrent && code === 'accept_d') shift.checklist = shift.checklist.map(item => item.code === 'accept_e' ? { ...item, comment: 'Правка другого окна', version: item.version + 1 } : item);
        savedFirst = true;
        await route.fulfill({ json: shift }); return;
      }
      if (request.method() === 'POST' && url.pathname.endsWith('/notes')) {
        const body = request.postDataJSON();
        shift = { ...shift, notes: [{ id: 'note', ...body }], version: shift.version + 1 };
        await route.fulfill({ status: 201, json: shift.notes[0] }); return;
      }
      if (request.method() === 'GET') {
        const blockers = shift.checklist.filter(item => item.answer === 'unanswered').map(item => ({ code: 'CHECKLIST_INCOMPLETE', item_code: item.code, message: 'Не заполнено: ' + item.label }));
        const fixtures = {
          '/api/v1/status': { environment: 'test', excel_sync_enabled: false, timezone: 'Europe/Moscow' },
          '/api/v1/auth/me': { user, device_trusted: true, pin_session: true },
          '/api/v1/auth/admin-options': [user],
          '/api/v1/auth/csrf': { csrf_token: 'synthetic-csrf' },
          '/api/v1/shifts/current': { shift },
          ['/api/v1/shifts/' + shift.id]: shift,
          ['/api/v1/shifts/' + shift.id + '/readiness']: { acceptance: { completed: 2 - blockers.length, total: 2, blockers }, closing: { completed: 0, total: 0, blockers: [] }, blockers, can_activate: false, can_prepare_handover: false },
        };
        if (Object.hasOwn(fixtures, url.pathname)) { await route.fulfill({ json: fixtures[url.pathname] }); return; }
      }
      unexpected.push(request.method() + ' ' + url.pathname); await route.abort(); return;
    }
    if (url.pathname !== '/' && !/^\/assets\/[A-Za-z0-9._-]+$/.test(url.pathname)) { unexpected.push(url.pathname); await route.abort(); return; }
    const name = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
    await route.fulfill({ body: await readFile(path.join(dist, name)), contentType: name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' });
  });
  const page = await context.newPage(); page.setDefaultTimeout(10000);
  const errors = []; page.on('pageerror', error => errors.push(error.message));
  await page.goto(origin);
  const first = page.locator('#checklist-accept_d');
  const second = page.locator('#checklist-accept_e');
  await first.waitFor();
  await page.getByRole('button', { name: 'К первому незавершённому пункту · приём', exact: true }).click();
  assert.ok(await first.locator('select').evaluate(element => element === document.activeElement), 'jump must focus the pending answer');
  await first.locator('select').selectOption('yes');
  await second.locator('textarea').fill('Несохранённое замечание второго пункта');
  assert.equal(await page.locator('form[data-checklist-dirty="true"]').count(), 2);
  assert.ok(await page.locator('fieldset.shift-actions').evaluate(element => element.disabled), 'responsibility actions stay disabled while drafts exist');
  assert.ok(await page.evaluate(() => { const event = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(event); return event.defaultPrevented; }), 'browser reload warning is enabled');
  page.once('dialog', dialog => dialog.dismiss());
  await page.getByRole('button', { name: 'Выйти / сменить сотрудника', exact: true }).click();
  assert.equal(logoutCalls, 0, 'cancel must not submit logout');
  await first.getByRole('button', { name: 'Сохранить ответ', exact: true }).click();
  await first.getByText('Сохранено', { exact: true }).waitFor();
  assert.ok(savedFirst);
  assert.equal(await second.locator('textarea').inputValue(), 'Несохранённое замечание второго пункта', 'saving another item preserves this draft');
  assert.equal(await page.locator('form[data-checklist-dirty="true"]').count(), 1);
  await page.getByRole('button', { name: 'К первому незавершённому пункту · приём', exact: true }).click();
  assert.ok(await second.locator('select').evaluate(element => element === document.activeElement), 'jump advances using saved server blockers');
  page.once('dialog', dialog => dialog.dismiss());
  await page.getByRole('button', { name: 'Перечитать сохранённые данные', exact: true }).click();
  assert.equal(await second.locator('textarea').inputValue(), 'Несохранённое замечание второго пункта');
  await second.locator('select').selectOption('yes');
  await second.getByRole('button', { name: 'Сохранить ответ', exact: true }).click();
  await second.getByText('Сохранено', { exact: true }).waitFor();
  assert.equal(await page.locator('form[data-checklist-dirty="true"]').count(), 0);
  assert.ok(!await page.locator('fieldset.shift-actions').evaluate(element => element.disabled));
  assert.equal(await page.evaluate(() => { const event = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(event); return event.defaultPrevented; }), false, 'warning clears after confirmed saves');
  const notes = page.locator('form').filter({ has: page.getByRole('button', { name: 'Сохранить замечание', exact: true }) });
  await notes.locator('textarea').fill('Замечание ещё не сохранено');
  assert.equal(await notes.getAttribute('data-checklist-dirty'), 'true');
  await notes.getByRole('button', { name: 'Сохранить замечание', exact: true }).click();
  await page.waitForFunction(() => !document.querySelector('form[data-checklist-dirty="true"]'));
  // Another snapshot must not erase a draft or upgrade its optimistic-lock version.
  concurrent = true;
  await second.locator('textarea').fill('Локальный черновик при конфликте');
  await first.locator('textarea').fill('Сохранить другой пункт');
  await first.getByRole('button', { name: 'Сохранить ответ', exact: true }).click();
  await first.getByText('Сохранено', { exact: true }).waitFor();
  assert.equal(await second.locator('textarea').inputValue(), 'Локальный черновик при конфликте');
  const expectedVersion = shift.checklist.find(item => item.code === 'accept_e').version - 1;
  await second.getByRole('button', { name: 'Сохранить ответ', exact: true }).click();
  await second.getByText('Ошибка. Перечитайте данные перед повтором.', { exact: true }).waitFor();
  assert.equal(lastVersion, expectedVersion, 'send the edit base version, not a newer snapshot version');
  page.once('dialog', dialog => dialog.accept());
  await page.getByRole('button', { name: 'Перечитать сохранённые данные', exact: true }).click();
  await page.waitForFunction(() => !document.querySelector('form[data-checklist-dirty="true"]'));
  assert.equal(await second.locator('textarea').inputValue(), 'Правка другого окна');
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'checklist fits mobile viewport');
  assert.deepEqual(unexpected, []);
  assert.deepEqual(errors, []);
  log('Checklist navigation, draft preservation, discard guards, version conflict and notes checks passed (mock API only).');
} finally { await browser.close(); }
