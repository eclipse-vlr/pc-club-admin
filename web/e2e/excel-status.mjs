/* global document, window */
// Real Chromium, intercepted API/static assets. No API process, DB or Google I/O.
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
const shift = { id: '22222222-2222-4222-8222-222222222222', owner_id: user.id, status: 'active', kind: 'day', business_date: '2026-10-20', template_version: 1, version: 2, checklist: [], notes: [], cash: null, handover_id: '', handover_parent_id: '' };
let state = { requested_version: 2, synced_version: 1, last_error_code: '', last_success_at: '2026-10-20T01:01:35Z', file_url: 'https://drive.google.com/file/d/synthetic-test-copy/view', sheet_name: 'Чек-лист приёма и сдачи смены', timezone: 'Europe/Moscow' };
let rejected = false;
const unexpected = [];
let activities = 0;
const browser = await chromium.launch({ executablePath: process.env.PC_CLUB_CHROMIUM || '/usr/bin/chromium', headless: true, args: ['--no-sandbox'] });
try {
  // This timezone deliberately differs from the club timezone.
  const context = await browser.newContext({ viewport: { width: 390, height: 844 }, timezoneId: 'America/Los_Angeles' });
  await context.route('**/*', async route => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== origin) { unexpected.push('external request'); await route.abort(); return; }
    if (url.pathname.startsWith('/api/')) {
      if (request.method() === 'POST' && url.pathname === '/api/v1/auth/activity') { activities++; await route.fulfill({ status: 204 }); return; }
      if (request.method() !== 'GET') { unexpected.push(request.method() + ' ' + url.pathname); await route.abort(); return; }
      const fixtures = {
        '/api/v1/status': { environment: 'test', excel_sync_enabled: true, timezone: 'Europe/Moscow' },
        '/api/v1/auth/me': { user, device_trusted: true, pin_session: true },
        '/api/v1/auth/admin-options': [user],
        '/api/v1/auth/csrf': { csrf_token: 'synthetic-csrf' },
        '/api/v1/shifts/current': { shift },
        ['/api/v1/shifts/' + shift.id + '/readiness']: { acceptance: { completed: 12, total: 12, blockers: [] }, closing: { completed: 0, total: 19, blockers: [] }, blockers: [] },
      };
      if (url.pathname === '/api/v1/shifts/' + shift.id + '/excel-sync') {
        await route.fulfill({ status: rejected ? 401 : 200, json: rejected ? { error: { code: 'AUTH_REQUIRED' } } : state }); return;
      }
      if (Object.hasOwn(fixtures, url.pathname)) { await route.fulfill({ json: fixtures[url.pathname] }); return; }
      unexpected.push(url.pathname); await route.abort(); return;
    }
    if (url.pathname !== '/' && !/^\/assets\/[A-Za-z0-9._-]+$/.test(url.pathname)) { unexpected.push(url.pathname); await route.abort(); return; }
    const name = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
    await route.fulfill({ body: await readFile(path.join(dist, name)), contentType: name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' });
  });
  const page = await context.newPage();
  page.setDefaultTimeout(10000);
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.clock.install();
  await page.goto(origin);
  const panel = page.getByRole('complementary', { name: 'Отправка чек-листа в Excel', exact: true });
  const status = panel.getByRole('status');
  await status.filter({ hasText: 'ожидают отправки' }).waitFor();
  await panel.getByText('Предыдущая успешная отправка', { exact: false }).waitFor();
  assert.match(await panel.locator('time').textContent(), /04:01:35/);
  const link = panel.getByRole('link', { name: 'Открыть тестовый Excel в новой вкладке', exact: true });
  assert.equal(await link.getAttribute('href'), state.file_url);
  assert.equal(await link.getAttribute('rel'), 'noopener noreferrer');
  assert.equal(await link.getAttribute('target'), '_blank');
  // Passive polling sees a successful newer version without prolonging the session.
  const previousActivities = activities;
  state = { ...state, synced_version: 2 };
  await page.clock.fastForward(10_100);
  await status.filter({ hasText: 'Сохранённые ответы отправлены' }).waitFor();
  assert.equal(activities, previousActivities, 'passive polling must not call auth/activity');
  await panel.getByText('Последняя успешная отправка', { exact: false }).waitFor();
  state = { ...state, last_error_code: 'EXCEL_ROW_EDITED' };
  await panel.getByRole('button', { name: 'Обновить статус Excel', exact: true }).click();
  await status.filter({ hasText: 'вручную' }).waitFor();
  await panel.getByText('Предыдущая успешная отправка', { exact: false }).waitFor();
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'Excel panel fits mobile width');
  if (process.env.PC_CLUB_SCREENSHOT_DIR) await panel.screenshot({ path: path.join(process.env.PC_CLUB_SCREENSHOT_DIR, 'excel-status-mobile.png') });
  // An unsuccessful status read must hide cached target/time metadata.
  rejected = true;
  await panel.getByRole('button', { name: 'Обновить статус Excel', exact: true }).click();
  await status.filter({ hasText: 'Не удалось проверить' }).waitFor();
  assert.equal(await panel.getByRole('link').count(), 0);
  assert.equal(await panel.locator('time').count(), 0);
  assert.deepEqual(unexpected, [], 'no real Google requests or business mutations');
  assert.deepEqual(errors, [], 'no browser errors');
  log('Excel link, club timezone, stale success, error, polling and mobile UI checks passed (mock API only).');
} finally { await browser.close(); }
