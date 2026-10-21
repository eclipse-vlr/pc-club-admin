/* global document, Event, innerWidth */
// Intercepted synthetic fixtures only. Actual PostgreSQL recovery is covered by handover.mjs.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { fileURLToPath, URL } from 'node:url';
import { log } from 'node:console';
import path from 'node:path';
import process from 'node:process';
import { chromium } from 'playwright-core';

const origin = 'http://127.0.0.1:54321';
const dist = fileURLToPath(new URL('../dist/', import.meta.url));
const user = { id: '11111111-1111-4111-8111-111111111111', display_name: 'Синтетический принимающий', roles: ['admin'], active: true };
const sender = { ...user, id: '22222222-2222-4222-8222-222222222222', display_name: 'Синтетический сдающий' };
const hID = '44444444-4444-4444-8444-444444444444';
const note = { id: '55555555-5555-4555-8555-555555555555', object_label: 'ПК 7', text: 'Записана поломка, ремонт ожидается' };
let shift = { id: '33333333-3333-4333-8333-333333333333', owner_id: user.id, status: 'opening', kind: 'night', business_date: '2026-10-20', template_version: 1, version: 1, notes: [], cash: null, handover_id: hID, handover_parent_id: 'previous', checklist: Array.from({ length: 12 }, (_, i) => ({ code: 'accept_' + i, phase: 'acceptance', label: 'Проверка ' + (i + 1), help_text: '', answer: 'yes', comment: '', equipment_check: false, equipment_issue: false, answered_by: user.id, version: 2 })) };
let handover = { id: hID, from_user_id: sender.id, to_user_id: user.id, to_shift_id: shift.id, revision: 1, version: 1, status: 'pending', expected_cash_kopecks: '1500000', counted_cash_kopecks: null, acknowledged_note_ids: [], snapshot: { notes: [note], checklist: [] } };
let failCurrent = false;
let failSnapshot = false;
let receipts = 0;
let accepts = 0;
let proofs = 0;
let releaseReceipt;
const receiptGate = new Promise(resolve => { releaseReceipt = resolve; });
let receiptReached;
const receiptStarted = new Promise(resolve => { receiptReached = resolve; });
const unexpected = [];
const browser = await chromium.launch({ executablePath: process.env.PC_CLUB_CHROMIUM || '/usr/bin/chromium', headless: true, args: ['--no-sandbox'] });
try {
  const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
  await context.route('**/*', async route => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== origin) { unexpected.push('external request'); await route.abort(); return; }
    if (url.pathname.startsWith('/api/')) {
      const endpoint = url.pathname.slice('/api/v1'.length);
      if (request.method() === 'GET') {
        if ((endpoint === '/shifts/current' && failCurrent) || (endpoint === '/handovers/' + hID && failSnapshot)) {
          await route.fulfill({ status: 503, json: { error: { code: 'SERVICE_UNAVAILABLE', message: 'Синтетический сбой чтения' } } }); return;
        }
        const fixtures = {
          '/status': { environment: 'test', excel_sync_enabled: false, timezone: 'Europe/Moscow' },
          '/auth/me': { user, device_trusted: true, pin_session: true },
          '/auth/admin-options': [user, sender],
          '/auth/csrf': { csrf_token: 'synthetic' },
          '/shifts/current': { shift },
          ['/handovers/' + hID]: handover,
          ['/shifts/' + shift.id + '/readiness']: { version: shift.version, acceptance: { completed: 12, total: 12, blockers: [] }, closing: { completed: 0, total: 19, blockers: [] }, blockers: [], can_activate: false, can_prepare_handover: false },
        };
        if (Object.hasOwn(fixtures, endpoint)) { await route.fulfill({ json: fixtures[endpoint] }); return; }
      }
      if (endpoint === '/auth/activity') { await route.fulfill({ status: 204 }); return; }
      if (endpoint === '/auth/reauth') { proofs++; await route.fulfill({ status: 201, json: { proof: 'synthetic-proof' } }); return; }
      if (endpoint === '/handovers/' + hID + '/receipt') {
        receipts++; receiptReached(); await receiptGate;
        const body = request.postDataJSON();
        assert.equal(body.counted_cash_kopecks, 1500000);
        assert.ok(request.headers()['idempotency-key']);
        handover = { ...handover, version: handover.version + 1, counted_cash_kopecks: '1500000' };
        shift = { ...shift, version: shift.version + 1 };
        await route.fulfill({ json: handover }); return;
      }
      if (endpoint === '/handovers/' + hID + '/acknowledgements') {
        handover = { ...handover, version: handover.version + 1, acknowledged_note_ids: [note.id] };
        shift = { ...shift, version: shift.version + 1 };
        await route.fulfill({ json: handover }); return;
      }
      if (endpoint === '/handovers/' + hID + '/accept') {
        accepts++;
        handover = { ...handover, status: 'accepted' }; shift = { ...shift, status: 'active', version: shift.version + 1 };
        await route.abort('connectionreset'); return;
      }
      unexpected.push(request.method() + ' ' + endpoint); await route.abort(); return;
    }
    if (url.pathname !== '/' && !/^\/assets\/[A-Za-z0-9._-]+$/.test(url.pathname)) { unexpected.push(url.pathname); await route.abort(); return; }
    const name = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
    await route.fulfill({ body: await readFile(path.join(dist, name)), contentType: name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' });
  });
  const page = await context.newPage(); page.setDefaultTimeout(10000);
  const errors = []; page.on('pageerror', error => errors.push(error.message));
  await page.goto(origin);
  const panel = page.getByRole('region', { name: 'Передача смены', exact: true });
  const accept = panel.getByRole('button', { name: 'Лично принять смену', exact: true });
  await accept.waitFor();
  assert.ok(await accept.isDisabled(), 'receipt and acknowledgement are required');
  await page.keyboard.press('Tab');
  assert.ok(await page.locator('.skip-link').evaluate(element => element === document.activeElement));
  await page.keyboard.press('Enter');
  assert.ok(await page.locator('#workspace').evaluate(element => element === document.activeElement), 'skip link moves keyboard focus');
  await panel.locator('#accept-blockers').getByRole('button', { name: 'Сохраните собственный пересчёт наличных.', exact: true }).click();
  assert.ok(await panel.locator('input[name="counted"]').evaluate(node => node === document.activeElement), 'receipt blocker focuses the actual field');
  const receipt = panel.locator('form').filter({ has: page.getByRole('button', { name: 'Сохранить мой пересчёт', exact: true }) });
  await receipt.locator('input').fill('15000');
  await receipt.evaluate(form => { form.requestSubmit(); form.requestSubmit(); });
  await receiptStarted;
  assert.ok(await receipt.locator('input').isDisabled(), 'inputs freeze while an operation is pending');
  failCurrent = true;
  releaseReceipt();
  await panel.getByRole('alert').filter({ hasText: 'Действие сохранено, но обновлённое состояние не получено' }).waitFor();
  assert.equal(receipts, 1, 'two synchronous submits send one monetary operation');
  failCurrent = false;
  await panel.getByRole('button', { name: 'Проверить сессию и перечитать передачу', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('input[name="counted"]')?.value === '');
  await panel.getByText('Сохранённый пересчёт получателя: 15', { exact: false }).waitFor();
  assert.equal(receipts, 1, 'reading after a saved action never resends it');
  const acknowledged = panel.getByRole('checkbox');
  await acknowledged.focus(); await page.keyboard.press('Space');
  await page.keyboard.press('Tab'); await page.keyboard.press('Enter');
  await page.waitForFunction(() => document.querySelector('input[name="notes"]')?.disabled === true);
  assert.ok(await accept.isEnabled(), 'readiness reflects saved data');
  await panel.getByLabel('Мой личный PIN', { exact: true }).fill('123456');
  await accept.click();
  await panel.getByRole('alert').filter({ hasText: 'Результат действия неизвестен' }).waitFor();
  assert.ok(await accept.isDisabled());
  assert.equal(await panel.getByLabel('Мой личный PIN', { exact: true }).inputValue(), '', 'PIN is cleared after submission');
  assert.ok(await panel.getByRole('alert').evaluate(element => element === document.activeElement), 'keyboard focus moves to recovery instructions');
  // Even synthetic submit events cannot bypass the unknown-outcome guard.
  await accept.locator('xpath=ancestor::form').evaluate(form => form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
  failCurrent = true;
  const check = panel.getByRole('button', { name: 'Проверить сессию и перечитать передачу', exact: true });
  await check.click();
  await panel.getByRole('alert').filter({ hasText: 'Не удалось проверить сессию' }).waitFor();
  assert.ok(await accept.isDisabled(), 'failed reconciliation cannot unlock submission');
  failCurrent = false; failSnapshot = true;
  await check.click();
  await panel.getByRole('alert').filter({ hasText: 'Снимок передачи не получен' }).waitFor();
  assert.equal(await panel.getByRole('button', { name: 'Лично принять смену', exact: true }).count(), 0);
  failSnapshot = false;
  await check.click();
  await panel.getByText('Редакция 1 · Принята', { exact: true }).waitFor();
  assert.equal(accepts, 1); assert.equal(proofs, 1);
  for (const width of [320, 390, 768, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'no page overflow at ' + width);
  }
  await page.evaluate(() => { document.documentElement.style.fontSize = '32px'; });
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'no overflow with doubled base font');
  await page.evaluate(() => { document.documentElement.style.fontSize = ''; });
  await page.setViewportSize({ width: 390, height: 844 });
  await panel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: '/tmp/pcclub-handover-recovery.png' });
  assert.deepEqual(unexpected, []); assert.deepEqual(errors, []);
  log('Handover double-submit guard, failed reconciliation, recovery, keyboard and responsive checks passed (mock API only).');
} finally { releaseReceipt(); await browser.close(); }
