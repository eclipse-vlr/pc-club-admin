/* global document, window, Event */
// Synthetic API fixtures in real Chromium. No real money, accounts or Drive requests.
import assert from 'node:assert/strict';
import { after, test } from 'node:test';
import { mkdir, readFile } from 'node:fs/promises';
import { fileURLToPath, URL } from 'node:url';
import path from 'node:path';
import process from 'node:process';
import { chromium } from 'playwright-core';

const origin = 'http://127.0.0.1:54321';
const dist = fileURLToPath(new URL('../dist/', import.meta.url));
const user = { id: '11111111-1111-4111-8111-111111111111', display_name: 'Администратор теста', roles: ['admin'], active: true };
const other = { ...user, id: '22222222-2222-4222-8222-222222222222', display_name: 'Другой администратор' };
const transferID = '44444444-4444-4444-8444-444444444444';
const browser = await chromium.launch({ executablePath: process.env.PC_CLUB_CHROMIUM || '/usr/bin/chromium', args: ['--no-sandbox'] });
after(() => browser.close());

async function fixture(t, receiving = false) {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const errors = [];
  let release;
  let reached;
  const gate = new Promise(resolve => { release = resolve; });
  const started = new Promise(resolve => { reached = resolve; });
  const state = { writes: [], hold: false, loseResponse: false, failRead: false, reads: 0, logouts: 0, cashChangesWithChecklist: false, refundsChangeWithChecklist: false };
  let shift = {
    id: '33333333-3333-4333-8333-333333333333', owner_id: user.id, status: receiving ? 'opening' : 'active', version: 1,
    kind: 'day', business_date: '2026-10-23', template_version: 1, notes: [],
    checklist: receiving ? [] : [{ code: 'close_a', phase: 'closing', label: 'Проверка зала', answer: 'yes', comment: '', equipment_check: false, equipment_issue: false, answered_by: user.id, version: 1 }],
    handover_id: receiving ? transferID : '', handover_parent_id: receiving ? 'previous' : '',
    cash: receiving ? null : {
      session: { version: 1, opening_cash_kopecks: '1000000', counted_opening_kopecks: '1000000', opening_confirmed_at: '2026-10-23T06:00:00Z', langame_cash_amount_kopecks: '0', extra_cash_refunds_kopecks: '0', extra_cash_expenses_kopecks: '0', counted_closing_kopecks: '1000000', movements: [] },
      summary: { expected_closing_kopecks: '1000000', difference_kopecks: '0', collection_total_kopecks: '0', policy_version: 'draft-v1', blockers: [] },
    },
  };
  let handover = { id: transferID, from_user_id: other.id, to_user_id: user.id, to_shift_id: shift.id, revision: 1, version: 1, status: 'pending', expected_cash_kopecks: '1000000', counted_cash_kopecks: null, acknowledged_note_ids: [], snapshot: { notes: [], checklist: [] } };
  t.after(async () => { release(); await context.close(); assert.deepEqual(errors, []); });
  await context.route('**/*', async route => {
    const request = route.request();
    const url = new URL(request.url());
    assert.equal(url.origin, origin, 'no external requests');
    const endpoint = url.pathname.slice('/api/v1'.length);
    if (url.pathname.startsWith('/api/')) {
      if (request.method() !== 'GET') {
        if (endpoint === '/auth/activity') return route.fulfill({ status: 204 });
        if (endpoint === '/auth/logout') { state.logouts++; return route.fulfill({ status: 204 }); }
        if (endpoint === `/shifts/${shift.id}/checklist/close_a`) {
          state.writes.push({ endpoint, body: request.postDataJSON() });
          shift = { ...shift, version: shift.version + 1, checklist: shift.checklist.map(entry => ({ ...entry, ...request.postDataJSON(), version: entry.version + 1 })) };
          if (state.cashChangesWithChecklist) shift = { ...shift, cash: { ...shift.cash, session: { ...shift.cash.session, version: shift.cash.session.version + 1, counted_closing_kopecks: '1000100' } } };
          if (state.refundsChangeWithChecklist) shift = { ...shift, cash: { ...shift.cash, session: { ...shift.cash.session, version: shift.cash.session.version + 1, extra_cash_refunds_kopecks: '100' } } };
          return route.fulfill({ json: shift });
        }
        assert.ok([`/shifts/${shift.id}/cash`, `/handovers/${transferID}/receipt`].includes(endpoint), endpoint);
        const body = request.postDataJSON();
        state.writes.push({ endpoint, body }); reached();
        if (state.hold) await gate;
        if (receiving) handover = { ...handover, version: handover.version + 1, counted_cash_kopecks: String(body.counted_cash_kopecks) };
        else shift = { ...shift, cash: { ...shift.cash, session: { ...shift.cash.session, ...Object.fromEntries(Object.entries(body).filter(([key]) => key.endsWith('_kopecks')).map(([key, value]) => [key, value === null ? null : String(value)])), version: shift.cash.session.version + 1 } } };
        shift = { ...shift, version: shift.version + 1 };
        return state.loseResponse ? route.abort('connectionreset') : route.fulfill({ json: receiving ? handover : shift.cash });
      }
      if (endpoint === '/shifts/current') state.reads++;
      if (endpoint === '/shifts/current' && state.failRead) return route.fulfill({ status: 503, json: { error: { code: 'SERVICE_UNAVAILABLE', message: 'Сбой чтения' } } });
      const phase = { completed: 0, total: 0, blockers: [] };
      const fixtures = {
        '/status': { environment: 'test', excel_sync_enabled: false, timezone: 'Europe/Moscow' },
        '/auth/me': { user, device_trusted: true, pin_session: true }, '/auth/admin-options': [user, other], '/auth/csrf': { csrf_token: 'synthetic-csrf' },
        '/shifts/current': { shift }, [`/shifts/${shift.id}/readiness`]: { version: shift.version, acceptance: phase, closing: phase, blockers: [], can_activate: false, can_prepare_handover: !receiving },
        [`/handovers/${transferID}`]: handover,
      };
      assert.ok(Object.hasOwn(fixtures, endpoint), endpoint);
      return route.fulfill({ json: fixtures[endpoint] });
    }
    assert.ok(url.pathname === '/' || /^\/assets\/[A-Za-z0-9._-]+$/.test(url.pathname));
    const name = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
    return route.fulfill({ body: await readFile(path.join(dist, name)), contentType: name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' });
  });
  const page = await context.newPage(); page.setDefaultTimeout(5000);
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(origin);
  const panel = page.locator(receiving ? '#shift-handover' : '#shift-cash');
  await panel.getByRole('button', { name: receiving ? 'Сохранить мой пересчёт' : 'Сохранить кассовый черновик', exact: true }).waitFor();
  return { page, panel, state, started, release };
}

async function warnsBeforeLeaving(page) {
  return page.evaluate(() => {
    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);
    return event.defaultPrevented;
  });
}

async function submitAttempts(form) {
  return form.evaluate(element => {
    let calls = 0;
    const fetch = window.fetch;
    window.fetch = (...args) => { if (args[0] === '/api/v1/auth/csrf') calls++; return fetch(...args); };
    try { element.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); }
    finally { window.fetch = fetch; }
    return calls;
  });
}

test('cash drafts warn before reload or logout and cancellation preserves the values', async t => {
  const { page, panel, state } = await fixture(t);
  const counted = panel.locator('input[name="counted"]');
  await counted.fill('123,45');
  assert.ok(await warnsBeforeLeaving(page), 'cash drafts participate in the page exit warning');
  const reads = state.reads;
  page.once('dialog', dialog => dialog.dismiss());
  await page.getByRole('button', { name: 'Перечитать сохранённые данные', exact: true }).click();
  assert.equal(state.reads, reads, 'cancelled discard does not fetch a new snapshot');
  assert.equal(await counted.inputValue(), '123,45');
  page.once('dialog', dialog => dialog.dismiss());
  await page.getByRole('button', { name: 'Выйти / сменить сотрудника', exact: true }).click();
  assert.equal(state.logouts, 0);
  assert.equal(await counted.inputValue(), '123,45');
  page.once('dialog', dialog => dialog.accept());
  await page.getByRole('button', { name: 'Перечитать сохранённые данные', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('#shift-cash input[name="counted"]')?.value === '10000,00');
  assert.equal(await warnsBeforeLeaving(page), false, 'confirmed discard clears the warning');
});

test('saving a checklist preserves an unsaved cash draft without interrupting the save', async t => {
  const { page, panel, state } = await fixture(t);
  await panel.locator('input[name="counted"]').fill('123,45');
  const entry = page.locator('#checklist-close_a');
  await entry.locator('summary').click();
  await entry.locator('textarea').fill('Новый комментарий');
  await entry.getByRole('button', { name: 'Сохранить ответ', exact: true }).click();
  await entry.getByText('Сохранено', { exact: true }).waitFor();
  assert.equal(state.writes.length, 1, 'checklist changes can be saved alongside a cash draft');
  assert.equal(await panel.locator('input[name="counted"]').inputValue(), '123,45');
  assert.equal(await entry.locator('textarea').inputValue(), 'Новый комментарий');
  assert.ok(await warnsBeforeLeaving(page));
});

test('a concurrent cash version preserves the draft and blocks overwriting the newer version', async t => {
  const { page, panel, state } = await fixture(t);
  await panel.locator('input[name="counted"]').fill('123,45');
  const entry = page.locator('#checklist-close_a');
  await entry.locator('summary').click(); await entry.locator('textarea').fill('Изменение чек-листа');
  state.cashChangesWithChecklist = true;
  await entry.getByRole('button', { name: 'Сохранить ответ', exact: true }).click();
  await entry.getByText('Сохранено', { exact: true }).waitFor();
  await panel.getByRole('alert').filter({ hasText: 'Касса изменилась в другом окне' }).waitFor();
  assert.equal(await panel.locator('input[name="counted"]').inputValue(), '123,45');
  assert.ok(await panel.getByRole('button', { name: 'Сохранить кассовый черновик', exact: true }).isDisabled());
  const form = panel.locator('form').filter({ has: page.getByRole('button', { name: 'Сохранить кассовый черновик', exact: true }) });
  assert.equal(await submitAttempts(form), 0, 'a newer server version cannot upgrade the draft base silently');
  page.once('dialog', dialog => dialog.accept());
  await page.getByRole('button', { name: 'Перечитать сохранённые данные', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('#shift-cash input[name="counted"]')?.value === '10001,00');
  assert.equal(await warnsBeforeLeaving(page), false);
});

test('reverting a cash edit clears its warning and a PIN alone is never a draft', async t => {
  const { page, panel } = await fixture(t);
  const counted = panel.locator('input[name="counted"]');
  await counted.fill('10001,00');
  assert.ok(await warnsBeforeLeaving(page));
  await counted.fill('10000,00');
  assert.equal(await warnsBeforeLeaving(page), false);
  const refunds = panel.getByRole('checkbox', { name: 'Дополнительных возвратов, не учтённых в LANGAME, не было', exact: true });
  await refunds.uncheck(); assert.ok(await warnsBeforeLeaving(page));
  await refunds.check(); assert.equal(await warnsBeforeLeaving(page), false);
  await panel.locator('input[type="password"]').fill('123456');
  assert.equal(await warnsBeforeLeaving(page), false, 'PINs are excluded from draft tracking');
});

test('fresh cash data updates zero-operation flags without overwriting saved refunds', async t => {
  const { page, panel, state } = await fixture(t);
  const entry = page.locator('#checklist-close_a');
  await entry.locator('summary').click(); await entry.locator('textarea').fill('Обновление снимка');
  state.refundsChangeWithChecklist = true;
  await entry.getByRole('button', { name: 'Сохранить ответ', exact: true }).click();
  await entry.getByText('Сохранено', { exact: true }).waitFor();
  await panel.locator('input[name="refunds"]:enabled').waitFor();
  assert.equal(await panel.locator('input[name="refunds"]').inputValue(), '1,00');
  assert.equal(await warnsBeforeLeaving(page), false, 'a server refresh is not a local edit');
  const saved = page.waitForResponse(response => response.request().method() === 'PATCH' && response.url().endsWith('/cash'));
  await panel.getByRole('button', { name: 'Сохранить кассовый черновик', exact: true }).click();
  await saved;
  assert.equal(state.writes.at(-1).body.extra_cash_refunds_kopecks, 100, 'the checkbox cannot replace saved refunds with zero');
});

test('an unsaved cash draft blocks preparing a handover and points back to cash', async t => {
  const { page, panel } = await fixture(t);
  const prepare = page.getByRole('button', { name: 'Подготовить передачу и выйти', exact: true });
  await prepare.locator(':scope:enabled').waitFor();
  await panel.locator('input[name="counted"]').fill('10001,00');
  assert.ok(await prepare.isDisabled(), 'saved readiness does not authorize ignoring an unsaved cash edit');
  await page.locator('#prepare-blockers').getByRole('button', { name: 'Сохраните несохранённые данные кассы.', exact: true }).click();
  assert.ok(await panel.evaluate(element => element.contains(document.activeElement)));
});

test('saving one cash form never silently discards another cash form', async t => {
  const { page, panel, state } = await fixture(t);
  await panel.locator('input[name="reason"]').fill('Черновик основания инкассации');
  page.once('dialog', dialog => dialog.dismiss());
  const form = panel.locator('form').filter({ has: page.getByRole('button', { name: 'Сохранить кассовый черновик', exact: true }) });
  assert.equal(await submitAttempts(form), 0, 'cancel prevents the monetary request');
  assert.equal(state.writes.length, 0);
  assert.equal(await panel.locator('input[name="reason"]').inputValue(), 'Черновик основания инкассации');
});

test('invalid cash input stays editable, preserves other fields and can be corrected', async t => {
  const { page, panel, state } = await fixture(t);
  const counted = panel.getByLabel('Мой пересчёт после изъятий · рубли', { exact: true });
  const langame = panel.getByLabel('Чистое изменение наличных по отчёту LANGAME · рубли', { exact: true });
  const save = panel.getByRole('button', { name: 'Сохранить кассовый черновик', exact: true });
  await langame.fill('125,50'); await counted.fill('1,234'); await save.click();
  await panel.getByRole('alert').waitFor();
  assert.ok(await save.isEnabled(), 'a local typo never requires reloading the cash data');
  assert.equal(await counted.inputValue(), '1,234');
  assert.equal(await langame.inputValue(), '125,50');
  assert.equal(await counted.getAttribute('aria-invalid'), 'true');
  assert.ok(await counted.evaluate(input => input === document.activeElement), 'focus points to the invalid amount');
  assert.equal(state.writes.length, 0);
  await counted.fill('10125,50');
  const saved = page.waitForResponse(response => response.request().method() === 'PATCH');
  await save.click(); await saved;
  await panel.locator('input[name="counted"]:enabled').waitFor();
  assert.equal(state.writes.length, 1);
  assert.equal(state.writes[0].body.counted_closing_kopecks, 1012550);
  assert.equal(state.writes[0].body.langame_cash_amount_kopecks, 12550);
  if (process.env.PC_CLUB_SCREENSHOT_DIR) {
    await mkdir(process.env.PC_CLUB_SCREENSHOT_DIR, { recursive: true });
    await panel.screenshot({ path: path.join(process.env.PC_CLUB_SCREENSHOT_DIR, 'cash-desktop.png') });
  }
  await page.setViewportSize({ width: 320, height: 900 });
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), 'cash forms fit a narrow screen');
  if (process.env.PC_CLUB_SCREENSHOT_DIR) await panel.screenshot({ path: path.join(process.env.PC_CLUB_SCREENSHOT_DIR, 'cash-mobile.png') });
});

test('cash inputs are frozen while a save is pending', async t => {
  const { panel, state, started, release } = await fixture(t);
  state.hold = true;
  await panel.getByRole('button', { name: 'Сохранить кассовый черновик', exact: true }).click();
  await started;
  assert.ok(await panel.locator('input[name="counted"]').isDisabled(), 'an edit cannot be silently lost when the response remounts the form');
  assert.ok(await panel.locator('input[name="amount"]').isDisabled(), 'other cash actions wait too');
  await panel.getByRole('status').filter({ hasText: 'Дождитесь результата' }).waitFor();
  release();
});

test('unknown cash results stay locked through a failed read and recover without another write', async t => {
  const { panel, state } = await fixture(t);
  state.loseResponse = true;
  await panel.getByRole('button', { name: 'Сохранить кассовый черновик', exact: true }).click();
  await panel.getByRole('alert').waitFor();
  assert.ok(await panel.locator('input[name="counted"]').isDisabled());
  state.failRead = true;
  await panel.getByRole('button', { name: 'Перечитать кассу', exact: true }).click();
  await panel.getByRole('alert').filter({ hasText: 'Не удалось перечитать кассу' }).waitFor();
  assert.ok(await panel.locator('input[name="counted"]').isDisabled());
  state.failRead = false;
  await panel.getByRole('button', { name: 'Перечитать кассу', exact: true }).click();
  await panel.locator('input[name="counted"]:enabled').waitFor();
  assert.equal(state.writes.length, 1, 'reading never repeats the monetary write');
});

test('invalid receiving amount can be corrected without reloading the handover', async t => {
  const { panel, state } = await fixture(t, true);
  const counted = panel.getByLabel('Мой пересчёт наличных · рубли', { exact: true });
  const save = panel.getByRole('button', { name: 'Сохранить мой пересчёт', exact: true });
  await counted.fill('-1'); await save.click();
  await panel.getByRole('alert').waitFor();
  assert.ok(await save.isEnabled(), 'a negative local amount never locks the handover');
  assert.equal(await counted.getAttribute('aria-invalid'), 'true');
  assert.ok(await counted.evaluate(input => input === document.activeElement));
  assert.equal(state.writes.length, 0);
  await counted.fill('10000,00'); await save.click();
  await panel.getByText('Сохранённый пересчёт получателя: 10', { exact: false }).waitFor();
  assert.equal(state.writes.length, 1);
});
