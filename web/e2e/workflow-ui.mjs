/* global document, window */
// Real Chromium; intercepted synthetic API only, never the user's API or Drive.
import assert from 'node:assert/strict';
import { readFile, mkdir } from 'node:fs/promises';
import { fileURLToPath, URL } from 'node:url';
import { log } from 'node:console';
import path from 'node:path';
import process from 'node:process';
import { chromium } from 'playwright-core';

const origin = 'http://127.0.0.1:54321';
const dist = fileURLToPath(new URL('../dist/', import.meta.url));
const user = { id: '11111111-1111-4111-8111-111111111111', display_name: 'Тестовый администратор', roles: ['admin'], active: true };
const receiver = { ...user, id: '33333333-3333-4333-8333-333333333333', display_name: 'Следующий администратор' };
let shift = {
  id: '22222222-2222-4222-8222-222222222222', owner_id: user.id, status: 'active', kind: 'day', business_date: '2026-10-21', version: 1, template_version: 1, handover_id: '', handover_parent_id: '', notes: [],
  checklist: ['accept_a', 'close_b', 'close_c'].map((code, i) => ({ code, phase: i ? 'closing' : 'acceptance', label: 'Проверка ' + (i + 1), help_text: '', answer: i ? 'unanswered' : 'yes', comment: '', equipment_check: false, equipment_issue: false, answered_by: i ? null : user.id, version: 1 })),
  cash: { session: { version: 1, opening_cash_kopecks: '0', opening_confirmed_at: '2026-10-21T06:00:00Z', counted_opening_kopecks: '0', langame_cash_amount_kopecks: '0', extra_cash_refunds_kopecks: '0', extra_cash_expenses_kopecks: '0', counted_closing_kopecks: '100', movements: [] }, summary: { expected_closing_kopecks: '0', difference_kopecks: '100', collection_total_kopecks: '0', policy_version: 'draft-v1', verified: false, blockers: [] } },
};
let delivered = 1;
let patches = 0;
let lostResponse = false;
let rejectNextReadiness = false;
let failReadinessAfterSave = false;
let mismatchedReadiness = false;
let noteRequired = false;
let rejectShiftRead = false;
let notePosts = 0;
let releaseSave;
let saveGate = null;
const unexpected = [];
const errors = [];
function readiness() {
  const phase = name => {
    const items = shift.checklist.filter(entry => entry.phase === name);
    const blockers = items.filter(entry => entry.answer !== 'yes').map(entry => ({ code: entry.answer === 'no' ? 'CHECKLIST_ANSWER_BLOCKS' : 'CHECKLIST_INCOMPLETE', item_code: entry.code, message: 'Не завершено: ' + entry.label }));
    if (name === 'closing' && noteRequired) blockers.push({ code: 'NOTE_REQUIRED', item_code: 'close_b', message: 'Запишите замечание: Проверка 2' });
    return { completed: items.filter(entry => entry.answer === 'yes').length, total: items.length, blockers };
  };
  const acceptance = phase('acceptance'); const closing = phase('closing');
  const blockers = [...acceptance.blockers, ...closing.blockers];
  if (shift.cash.summary.difference_kopecks !== '0') blockers.push({ code: 'CASH_MISMATCH', message: 'Расхождение кассы: 1 ₽. Сдать смену нельзя.' });
  return { version: shift.version + (mismatchedReadiness ? 1 : 0), acceptance, closing, blockers, can_activate: false, can_prepare_handover: blockers.length === 0 };
}
const browser = await chromium.launch({ executablePath: process.env.PC_CLUB_CHROMIUM || '/usr/bin/chromium', args: ['--no-sandbox'] });
try {
  const context = await browser.newContext({ viewport: { width: 1366, height: 900 }, reducedMotion: 'reduce' });
  await context.route('**/*', async route => {
    const request = route.request(); const url = new URL(request.url());
    assert.equal(url.origin, origin, 'no external requests');
    if (url.pathname.startsWith('/api/')) {
      const endpoint = url.pathname.slice('/api/v1'.length);
      if (endpoint === '/auth/activity') return route.fulfill({ status: 204 });
      if (request.method() === 'POST' && endpoint.endsWith('/notes')) {
        notePosts++; noteRequired = false;
        const note = { id: 'synthetic-note', ...request.postDataJSON(), version: 1 };
        shift = { ...shift, version: shift.version + 1, notes: [note] };
        return route.fulfill({ status: 201, json: note });
      }
      if (request.method() === 'PATCH' && endpoint.includes('/checklist/')) {
        patches++; if (saveGate) await saveGate;
        const body = request.postDataJSON(); const code = endpoint.split('/').at(-1);
        shift = { ...shift, version: shift.version + 1, checklist: shift.checklist.map(entry => entry.code === code ? { ...entry, ...body, version: entry.version + 1, answered_by: user.id } : entry) };
        if (failReadinessAfterSave) { rejectNextReadiness = true; failReadinessAfterSave = false; }
        if (lostResponse) { lostResponse = false; return route.abort('connectionreset'); }
        return route.fulfill({ json: shift });
      }
      if (request.method() === 'PATCH' && endpoint.endsWith('/cash')) {
        shift = { ...shift, version: shift.version + 1, cash: { ...shift.cash, session: { ...shift.cash.session, version: shift.cash.session.version + 1, counted_closing_kopecks: '0' }, summary: { ...shift.cash.summary, difference_kopecks: '0' } } };
        return route.fulfill({ json: shift.cash });
      }
      if (request.method() === 'GET') {
        if (endpoint === '/shifts/' + shift.id && rejectShiftRead) {
          rejectShiftRead = false; return route.fulfill({ status: 503, json: { error: { code: 'SERVICE_UNAVAILABLE' } } });
        }
        if (endpoint.endsWith('/readiness')) {
          if (rejectNextReadiness) { rejectNextReadiness = false; return route.fulfill({ status: 503, json: { error: { code: 'SERVICE_UNAVAILABLE' } } }); }
          return route.fulfill({ json: readiness() });
        }
        if (endpoint.endsWith('/excel-sync')) return route.fulfill({ json: { requested_version: shift.version, synced_version: delivered, last_error_code: '', last_success_at: '2026-10-21T06:01:00Z', timezone: 'Europe/Moscow' } });
        const fixtures = {
          '/status': { environment: 'test', excel_sync_enabled: true, timezone: 'Europe/Moscow' },
          '/auth/me': { user, device_trusted: true, pin_session: true }, '/auth/admin-options': [user, receiver], '/auth/csrf': { csrf_token: 'synthetic-csrf' },
          '/shifts/current': { shift }, ['/shifts/' + shift.id]: shift,
        };
        if (Object.hasOwn(fixtures, endpoint)) return route.fulfill({ json: fixtures[endpoint] });
      }
      unexpected.push(request.method() + ' ' + endpoint); return route.abort();
    }
    assert.ok(url.pathname === '/' || /^\/assets\/[A-Za-z0-9._-]+$/.test(url.pathname));
    const name = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
    return route.fulfill({ body: await readFile(path.join(dist, name)), contentType: name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' });
  });
  const page = await context.newPage(); page.setDefaultTimeout(10_000); page.on('pageerror', error => errors.push(error.message));
  await page.goto(origin);
  const prepare = page.getByRole('button', { name: 'Подготовить передачу и выйти', exact: true }); await prepare.waitFor();
  const first = page.locator('#checklist-close_b'); const second = page.locator('#checklist-close_c');
  const storage = page.getByRole('group', { name: 'Сохранение в приложении', exact: true });
  const excel = page.getByRole('group', { name: 'Отправка в Excel', exact: true });
  assert.equal(await page.locator('#shift-acceptance').evaluate(node => node.open), false);
  assert.equal(await page.locator('#shift-closing').evaluate(node => node.open), true);
  assert.equal(await prepare.getAttribute('aria-describedby'), 'prepare-blockers');
  await page.locator('#shift-closing > summary').focus(); await page.keyboard.press('Enter');
  await page.locator('#prepare-blockers').getByRole('button', { name: 'Не завершено: Проверка 2', exact: true }).click();
  assert.ok(await first.locator('select').evaluate(node => node === document.activeElement), 'blocked action reveals and focuses the field');
  await first.locator('select').selectOption('yes');
  await second.locator('textarea').fill('Черновик второго пункта');
  await excel.getByText('Предыдущая сохранённая версия отправлена', { exact: false }).waitFor();
  await page.locator('#shift-closing > summary').click();
  await page.locator('#prepare-blockers').getByRole('button', { name: 'Сохраните ответ: Проверка 3', exact: true }).click();
  assert.equal(await second.locator('textarea').inputValue(), 'Черновик второго пункта');
  assert.equal(patches, 0, 'navigation never submits a draft');
  saveGate = new Promise(resolve => { releaseSave = resolve; });
  await first.getByRole('button', { name: 'Сохранить ответ', exact: true }).click();
  await storage.getByText('Сохраняем изменения в приложении…', { exact: true }).waitFor();
  assert.ok(await prepare.isDisabled()); releaseSave(); saveGate = null;
  await first.getByText('Сохранено', { exact: true }).waitFor();
  assert.equal(await first.locator('details').evaluate(node => node.open), false, 'completed item folds');
  assert.equal(await second.locator('textarea').inputValue(), 'Черновик второго пункта', 'save does not erase another draft');
  await second.locator('select').selectOption('yes'); await second.getByRole('button', { name: 'Сохранить ответ', exact: true }).click();
  await storage.getByText('Ответы сохранены в приложении.', { exact: true }).waitFor();
  await page.getByRole('button', { name: 'Обновить статус Excel', exact: true }).click();
  await excel.getByText('ожидают отправки', { exact: false }).waitFor();
  await page.locator('#prepare-blockers').getByRole('button', { name: 'Расхождение кассы: 1 ₽. Сдать смену нельзя.', exact: true }).click();
  assert.ok(await page.getByLabel('Чистое изменение наличных по отчёту LANGAME · рубли', { exact: true }).evaluate(node => node === document.activeElement));
  await page.getByLabel('Мой пересчёт после изъятий · рубли', { exact: true }).fill('0');
  await page.getByRole('button', { name: 'Сохранить кассовый черновик', exact: true }).click();
  await page.waitForFunction(() => !document.querySelector('button[aria-describedby="prepare-blockers"]'));
  assert.ok(await prepare.isEnabled(), 'only saved server readiness enables preparation');
  delivered = shift.version; await page.getByRole('button', { name: 'Обновить статус Excel', exact: true }).click();
  await excel.getByText('Сохранённые ответы отправлены в Excel.', { exact: true }).waitFor();
  mismatchedReadiness = true;
  await page.getByRole('button', { name: 'Перечитать сохранённые данные', exact: true }).click();
  await page.getByRole('alert').filter({ hasText: 'Смена изменилась в другом окне' }).waitFor();
  assert.ok(await prepare.isDisabled(), 'readiness for another snapshot cannot authorize the shown shift');
  mismatchedReadiness = false;
  await page.getByRole('button', { name: 'Перечитать сохранённые данные', exact: true }).click();
  await page.waitForFunction(() => !document.querySelector('button[aria-describedby="prepare-blockers"]'));
  await first.locator('summary').click(); await first.locator('textarea').fill('Подтверждённая запись, ошибка readiness'); failReadinessAfterSave = true;
  await first.getByRole('button', { name: 'Сохранить ответ', exact: true }).click();
  await storage.getByText('Ответы сохранены в приложении.', { exact: true }).waitFor();
  await page.getByRole('alert').filter({ hasText: 'Ответ сохранён в приложении. Не удалось проверить' }).waitFor(); assert.ok(await prepare.isDisabled());
  await page.getByRole('button', { name: 'Перечитать сохранённые данные', exact: true }).click();
  await first.getByText('Сохранено', { exact: true }).waitFor();
  await first.locator('summary').click(); await first.locator('textarea').fill('Сервер сохранил, ответ потерян'); lostResponse = true;
  const previousPatches = patches; await first.getByRole('button', { name: 'Сохранить ответ', exact: true }).click();
  await storage.getByText('Результат сохранения неизвестен.', { exact: false }).waitFor();
  assert.equal(patches, previousPatches + 1); assert.ok(await prepare.isDisabled());
  page.once('dialog', dialog => dialog.accept()); await page.getByRole('button', { name: 'Перечитать сохранённые данные', exact: true }).click();
  await storage.getByText('Ответы сохранены в приложении.', { exact: true }).waitFor();
  assert.equal(await first.locator('textarea').inputValue(), 'Сервер сохранил, ответ потерян'); assert.equal(patches, previousPatches + 1, 'reconciliation never resends');
  noteRequired = true; await page.getByRole('button', { name: 'Перечитать сохранённые данные', exact: true }).click();
  await page.locator('#prepare-blockers').getByRole('button', { name: 'Запишите замечание: Проверка 2', exact: true }).click();
  assert.ok(await page.locator('#shift-notes select[name="code"]').evaluate(node => node === document.activeElement));
  for (const width of [1366, 390]) {
    await page.setViewportSize({ width, height: 900 }); assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
    if (process.env.PC_CLUB_SCREENSHOT_DIR) { await mkdir(process.env.PC_CLUB_SCREENSHOT_DIR, { recursive: true }); await page.locator('#shift-handover').screenshot({ path: path.join(process.env.PC_CLUB_SCREENSHOT_DIR, `inline-blockers-${width}.png`) }); }
  }
  const noteForm = page.locator('#shift-notes form');
  await noteForm.locator('select[name="code"]').selectOption('close_b');
  await noteForm.locator('select[name="category"]').selectOption('equipment');
  await noteForm.locator('input[name="object"]').fill('Синтетический ПК');
  await noteForm.locator('textarea').fill('Тестовая запись с подтверждённым ответом и потерянным чтением');
  rejectShiftRead = true; await noteForm.getByRole('button', { name: 'Сохранить замечание', exact: true }).click();
  await storage.getByText('Запись сохранена в приложении.', { exact: false }).waitFor();
  assert.equal(notePosts, 1); assert.ok(await noteForm.getByRole('button').isDisabled());
  assert.equal(await noteForm.locator('textarea').inputValue(), '', 'a confirmed note is not retained as an unsaved copy');
  await page.getByRole('button', { name: 'Перечитать сохранённые данные', exact: true }).click();
  await storage.getByText('Ответы сохранены в приложении.', { exact: true }).waitFor(); assert.equal(notePosts, 1, 'a read failure cannot duplicate a confirmed note');
  assert.deepEqual(unexpected, []); assert.deepEqual(errors, []);
  log('Workflow UI checks passed: inline blockers/focus, folding/drafts, storage vs Excel, delayed/lost responses and readiness recovery.');
} finally { await browser.close(); }
