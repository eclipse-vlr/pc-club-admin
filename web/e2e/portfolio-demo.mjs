/* global document */
import assert from 'node:assert/strict';
import process from 'node:process';
import console from 'node:console';
import { mkdir, copyFile, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath, URL } from 'node:url';
import { chromium } from 'playwright-core';
import { origin, demo, apiClient } from '../../scripts/portfolio-demo.mjs';

const root = fileURLToPath(new URL('../../', import.meta.url));
const output = path.join(root, 'output/playwright/portfolio-demo');
const screenshots = path.join(root, 'assets/demo');
const recording = process.argv.includes('--record');
assert.ok(process.argv.slice(2).every(arg => arg === '--record'));
await mkdir(output, { recursive: true }); await mkdir(screenshots, { recursive: true });
const status = await apiClient()('/status');
assert.equal(status.environment, 'test'); assert.equal(status.excel_sync_enabled, false);
const browser = await chromium.launch({ executablePath: process.env.PC_CLUB_CHROMIUM || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true, slowMo: recording ? 70 : 0 });
const errors = [];
let club;
let activePage;
let video;
const started = Date.now();
const checks = [];
const check = message => { checks.push(message); console.log(message); };
async function scene(page, title, duration = 2200) {
  if (!recording || !page.video()) return;
  await page.evaluate(text => {
    let caption = document.getElementById('demo-video-caption');
    if (!caption) {
      caption = document.createElement('div');
      caption.id = 'demo-video-caption';
      caption.style.cssText = 'position:fixed;bottom:16px;left:24px;right:24px;z-index:99999;padding:14px 20px;border:1px solid #8b88ff;border-radius:12px;background:#101329f2;color:#fff;font:600 20px/1.4 system-ui;pointer-events:none;text-align:center';
      document.body.append(caption);
    }
    caption.textContent = text;
  }, title);
  await page.waitForTimeout(duration);
}
async function screenshot(page, locator, name, viewportOnly = false) {
  if (recording && !page.video()) return;
  if (recording || viewportOnly) {
    const focus = recording && name === 'handover-blocked' ? locator.locator('#accept-blockers') : recording && name === 'audit' ? locator.locator('ol') : locator;
    await focus.evaluate(element => element.scrollIntoView({ block: 'start' }));
    // Leave the app's sticky navigation above the content being demonstrated.
    await page.mouse.wheel(0, -100);
    if (recording) await page.waitForTimeout(6500);
    else await page.screenshot({ path: path.join(screenshots, name + '.png'), animations: 'disabled' });
  } else {
    // Fit the entire section in a real viewport so sticky navigation does not
    // overlap the middle of a screenshot captured beyond the viewport.
    const bounds = await locator.boundingBox();
    await page.setViewportSize({ width: 1280, height: Math.max(900, Math.ceil(bounds.height) + 240) });
    await locator.scrollIntoViewIfNeeded();
    await page.mouse.wheel(0, -100);
    await locator.screenshot({ path: path.join(screenshots, name + '.png'), animations: 'disabled' });
    await page.setViewportSize({ width: 1280, height: 900 });
  }
}
async function managerLogin(page) {
  await page.getByRole('button', { name: 'Управляющий · пароль', exact: true }).click();
  await page.getByLabel('Логин управляющего', { exact: true }).fill(demo.login);
  await page.getByLabel('Пароль', { exact: true }).fill(demo.password);
  await page.getByRole('button', { name: 'Войти как управляющий', exact: true }).click();
  await page.getByRole('heading', { level: 1, name: demo.manager }).waitFor();
}
try {
  const managerContext = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const manager = await managerContext.newPage(); manager.setDefaultTimeout(15000);
  manager.on('pageerror', error => errors.push(error.message));
  await manager.goto(origin); await managerLogin(manager);
  const first = await managerContext.request.get(origin + '/api/v1/shifts/current');
  const initial = (await first.json()).shift;
  assert.equal(initial?.status, 'opening', 'Start from reset + up; capture never resets a database automatically');
  await manager.getByLabel('Повторите пароль управляющего', { exact: true }).fill(demo.password);
  await manager.getByRole('button', { name: 'Создать код на 10 минут', exact: true }).click();
  await manager.locator('code.code').waitFor();
  const enrollment = await manager.locator('code.code').textContent();
  club = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  let page = await club.newPage(); page.setDefaultTimeout(15000);
  activePage = page;
  video = page.video(); page.on('pageerror', error => errors.push(error.message));
  await page.goto(origin);
  await page.getByText('Демонстрация. Вымышленные данные', { exact: true }).waitFor();
  await scene(page, 'PC Club Admin · передача смены. Все имена и суммы вымышлены.', 3000);
  await page.locator('summary').filter({ hasText: 'Зарегистрировать браузер клуба' }).click();
  await page.getByLabel('Одноразовый код', { exact: true }).fill(enrollment);
  await page.getByLabel('Название браузера', { exact: true }).fill('Демоклуб Орион · браузер показа');
  await page.getByRole('button', { name: 'Зарегистрировать', exact: true }).click();
  await page.locator('select[name="user"]').waitFor();
  async function login(employee) {
    await page.locator('select[name="user"]').selectOption({ label: employee.name });
    await page.getByLabel('Личный PIN', { exact: true }).fill(employee.pin);
    await page.getByRole('button', { name: 'Войти по PIN', exact: true }).click();
    await page.getByRole('heading', { level: 1, name: employee.name }).waitFor();
  }
  async function checklist(phase, count) {
    const area = page.locator('#shift-' + phase);
    if (!(await area.getAttribute('open') !== null)) await area.locator('summary').first().click();
    for (let index = 0; index < count; index++) {
      const form = area.locator('form.checklist-item').nth(index);
      const code = await form.getAttribute('id');
      await form.locator('select[name="answer"]').selectOption(code?.endsWith('conflicts_recorded') ? 'no' : 'yes');
      if (code?.endsWith('close_x')) await form.locator('input[name="issue"]').check();
      const saved = page.waitForResponse(response => response.url().includes('/checklist/') && response.request().method() === 'PATCH' && response.status() === 200);
      await form.getByRole('button', { name: 'Сохранить ответ', exact: true }).click(); await saved;
      await form.getByText('Сохранено', { exact: true }).waitFor();
      if (recording && video) await page.waitForTimeout(1100);
    }
  }
  await login(demo.employees[0]);
  await scene(page, '1. Алексей принимает смену: обязательный чек-лист и пересчёт наличных.');
  const activate = page.getByRole('button', { name: 'Подтвердить первый приём', exact: true });
  await activate.waitFor(); assert.ok(await activate.isDisabled()); check('Empty mandatory checklist blocks first acceptance');
  await checklist('acceptance', 12);
  await screenshot(page, page.locator('#shift-acceptance'), 'checklist');
  await page.getByLabel('Мой пересчёт в рублях', { exact: true }).fill('10000');
  await page.getByLabel('Личный PIN для этого действия', { exact: true }).fill(demo.employees[0].pin);
  await activate.click(); await page.locator('.shift-state').filter({ hasText: 'Активная смена' }).waitFor();
  await scene(page, '2. Сдача смены: ответы сохраняются; неисправности передаются следующему сотруднику.');
  await checklist('closing', 19);
  if (recording) {
    // Prepare the first shift normally, then record the handover itself.
    // Reuse the real browser session; no API responses or database state are replaced.
    const storageState = await club.storageState();
    await club.close();
    club = await browser.newContext({ storageState, viewport: { width: 1280, height: 900 }, recordVideo: { dir: output, size: { width: 1280, height: 900 } } });
    page = await club.newPage(); page.setDefaultTimeout(15000);
    activePage = page; video = page.video();
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(origin);
    await page.getByRole('heading', { level: 1, name: demo.employees[0].name }).waitFor();
    await scene(page, 'PC Club Admin · передача смены. Алексей завершил чек-листы; все данные вымышлены.', 3000);
  }
  const notes = page.locator('#shift-notes');
  for (const [object, text] of [
    ['ПК №7', 'Не работает левый наушник. Поломка записана, ремонт ожидается.'],
    ['ПК №12', 'Заедает колесо мыши. Запасная мышь подготовлена для замены.'],
  ]) {
    await notes.locator('select[name="category"]').selectOption('equipment');
    await notes.locator('select[name="code"]').selectOption('close_x');
    await notes.locator('input[name="object"]').fill(object);
    await notes.locator('textarea[name="text"]').fill(text);
    await notes.getByRole('button', { name: 'Сохранить замечание', exact: true }).click();
    await notes.locator('li').filter({ hasText: object }).waitFor();
    await page.waitForFunction(() => document.querySelector('#shift-notes textarea')?.value === '');
    if (recording) await page.waitForTimeout(1500);
  }
  const cash = page.locator('.cash-panel');
  await cash.locator('input[name="langame"]').fill('5000');
  await cash.locator('input[name="refunds"]').fill('0');
  await cash.locator('input[name="expenses"]').fill('0');
  await cash.locator('input[name="counted"]').fill('15000');
  const cashSaved = page.waitForResponse(response => response.url().endsWith('/cash') && response.request().method() === 'PATCH');
  await cash.getByRole('button', { name: 'Сохранить кассовый черновик', exact: true }).click();
  assert.equal((await cashSaved).status(), 200);
  await scene(page, '2. Кассовый черновик: 10 000 ₽ + 5 000 ₽ = 15 000 ₽ для передачи.', 3000);
  await page.waitForFunction(() => [...document.querySelectorAll('button')].some(button => button.textContent === 'Подготовить передачу и выйти' && !button.disabled));
  const prepare = page.locator('form').filter({ has: page.getByRole('button', { name: 'Подготовить передачу и выйти', exact: true }) });
  await prepare.locator('select[name="receiver"]').selectOption({ label: demo.employees[1].name });
  await prepare.getByLabel('Мой личный PIN', { exact: true }).fill(demo.employees[0].pin);
  const prepared = page.waitForResponse(response => response.url().endsWith('/handovers') && response.request().method() === 'POST' && response.status() === 201);
  await prepare.getByRole('button', { name: 'Подготовить передачу и выйти', exact: true }).click(); await prepared;
  await page.getByRole('heading', { name: 'Вход в приложение', exact: true }).waitFor();
  await scene(page, '3. Алексей вышел. До личного приёма Марией ответственность остаётся у него.');
  check('Sender preparation ends the personal session');
  await login(demo.employees[1]);
  await scene(page, '4. Мария входит со своим PIN, проверяет чек-лист и читает замечания.');
  await page.getByText('Редакция 1 · Ожидает приёма', { exact: true }).waitFor();
  await checklist('acceptance', 12);
  const transfer = page.getByRole('region', { name: 'Передача смены', exact: true });
  for (const checkbox of await transfer.locator('input[name="notes"]').all()) await checkbox.check();
  await transfer.getByRole('button', { name: 'Сохранить ознакомление', exact: true }).click();
  await page.waitForFunction(() => document.querySelector('input[name="notes"]')?.disabled === true);
  async function count(amount) {
    await page.getByLabel('Мой пересчёт наличных · рубли', { exact: true }).fill(amount);
    const saved = page.waitForResponse(response => response.url().endsWith('/receipt') && response.request().method() === 'PATCH' && response.status() === 200);
    await page.getByRole('button', { name: 'Сохранить мой пересчёт', exact: true }).click(); await saved;
    await page.waitForFunction(() => document.querySelector('input[name="counted"]')?.value === '');
  }
  await count('14999,99');
  await page.getByText('Ваш пересчёт не совпадает', { exact: false }).waitFor();
  assert.ok(await page.getByRole('button', { name: 'Лично принять смену', exact: true }).isDisabled());
  await scene(page, '5. Пересчёт 14 999,99 ₽: расхождение в одну копейку блокирует приём.', 3000);
  await screenshot(page, transfer, 'handover-blocked'); check('One kopeck mismatch blocks the receiver');
  const managerDashboard = manager.getByRole('region', { name: 'Панель руководителя', exact: true });
  const dashboardUpdated = manager.waitForResponse(response => response.url().endsWith('/manager/dashboard') && response.status() === 200);
  await managerDashboard.getByRole('button', { name: 'Обновить панель', exact: true }).click(); await dashboardUpdated;
  await managerDashboard.getByText('Пересчёт получателя не совпадает; приём заблокирован.', { exact: true }).waitFor();
  if (!recording) {
    await screenshot(manager, managerDashboard, 'manager-dashboard');
    await manager.getByRole('link', { name: 'Обзор клуба', exact: true }).click();
    await manager.screenshot({ path: path.join(output, 'workspace-pending.png'), animations: 'disabled' });
  }
  await count('15000');
  await scene(page, '6. Мария исправляет сумму на 15 000 ₽ и подтверждает приём личным PIN.', 3000);
  await page.getByLabel('Мой личный PIN', { exact: true }).fill(demo.employees[1].pin);
  await page.getByRole('button', { name: 'Лично принять смену', exact: true }).click();
  await page.locator('.shift-state').filter({ hasText: 'Активная смена' }).waitFor();
  await scene(page, 'Смена принята. Теперь ответственный — Мария, начальный остаток — 15 000 ₽.');
  await page.locator('#shift-closing > summary').click();
  await screenshot(page, page.locator('#current-shift'), 'handover-accepted', true); check('Receiver accepts personally after matching the amount');
  await page.getByRole('button', { name: 'Выйти / сменить сотрудника', exact: true }).click();
  await page.getByRole('heading', { name: 'Вход в приложение', exact: true }).waitFor();
  await managerLogin(page);
  await scene(page, '7. Управляющий проверяет нового ответственного, историю и журнал действий.');
  const dashboard = page.getByRole('region', { name: 'Панель руководителя', exact: true });
  await dashboard.getByRole('heading', { name: 'Ответственный: ' + demo.employees[1].name, exact: true }).waitFor();
  if (recording) await screenshot(page, dashboard, 'manager-dashboard');
  await page.locator('summary').filter({ hasText: 'История смен' }).click();
  await page.getByRole('button', { name: 'Просмотреть смену 2026-11-02 · Алексей Демо · День', exact: true }).click();
  const detail = page.getByRole('region', { name: 'Детали смены', exact: true });
  await detail.getByRole('button', { name: 'Просмотреть передачу · редакция 1', exact: true }).click();
  const snapshot = page.getByRole('region', { name: 'Снимок передачи', exact: true });
  await snapshot.getByRole('heading', { name: 'Снимок передачи · Принята · редакция 1', exact: true }).waitFor();
  await screenshot(page, snapshot, 'history');
  await page.locator('summary').filter({ hasText: 'Журнал действий' }).click();
  const audit = page.getByRole('region', { name: 'Журнал действий', exact: true });
  await audit.getByLabel('Действие (точный код)', { exact: true }).fill('handover_accepted');
  await audit.getByRole('button', { name: 'Показать журнал', exact: true }).click();
  await audit.getByText('Событий на странице: 1', { exact: true }).waitFor();
  await scene(page, 'В журнале ровно один приём. Показ — на настоящих API и PostgreSQL, без рабочих данных.', 3000);
  await screenshot(page, audit, 'audit'); check('Manager sees the new owner, accepted snapshot and exactly one acceptance audit event');
  assert.deepEqual(errors, []);
  await club.close(); club = null;
  if (video) await copyFile(await video.path(), path.join(output, 'walkthrough.webm'));
  await writeFile(path.join(output, recording ? 'recording.json' : 'verification.json'), JSON.stringify({
    base_revision: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(),
    working_tree_dirty: execFileSync('git', ['status', '--porcelain'], { cwd: root, encoding: 'utf8' }).trim().length > 0,
    recorded_at: new Date().toISOString(), duration_seconds: Math.round((Date.now() - started) / 1000), checks, browser_errors: errors,
  }, null, 2) + '\n');
} catch (error) {
  if (activePage && !activePage.isClosed()) {
    await activePage.screenshot({ path: path.join(output, 'failure.png'), fullPage: true });
    await writeFile(path.join(output, 'failure.txt'), await activePage.locator('body').innerText());
  }
  throw error;
} finally { await club?.close(); await browser.close(); }
