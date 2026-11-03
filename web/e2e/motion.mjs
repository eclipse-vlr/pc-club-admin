/* global document, window, Element, Storage */
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { fileURLToPath, URL } from 'node:url';
import path from 'node:path';
import process from 'node:process';
import { log } from 'node:console';
import { chromium } from 'playwright-core';

const origin = 'http://127.0.0.1:54321';
const dist = fileURLToPath(new URL('../dist/', import.meta.url));
const user = { id: '00000000-0000-4000-8000-000000000001', display_name: 'Управляющий проверки движения', roles: ['manager'], active: true, version: 1 };
const browser = await chromium.launch({ executablePath: process.env.PC_CLUB_CHROMIUM || '/usr/bin/chromium', headless: true });
try {
  async function context(authenticated = false) {
    const result = await browser.newContext({ viewport: { width: 1280, height: 900 }, reducedMotion: 'no-preference' });
    await result.addInitScript(() => {
      window.interfaceAnimations = [];
      const original = Element.prototype.animate;
      Element.prototype.animate = function (frames, options) {
        const animation = original.call(this, frames, options);
        window.interfaceAnimations.push({ id: this.id, tag: this.tagName, animation });
        return animation;
      };
    });
    await result.route('**/*', async route => {
      const url = new URL(route.request().url());
      assert.equal(url.origin, origin, 'no external requests');
      if (url.pathname.startsWith('/api/')) {
        const routeName = url.pathname.slice('/api/v1'.length);
        if (routeName === '/status') return route.fulfill({ json: { environment: 'test', timezone: 'Europe/Moscow', excel_sync_enabled: false } });
        if (routeName === '/auth/csrf') return route.fulfill({ json: { csrf_token: 'a'.repeat(64) } });
        if (routeName === '/auth/activity') return route.fulfill({ status: 204, body: '' });
        if (routeName === '/auth/me') return route.fulfill(authenticated ? { json: { user, device_trusted: false, pin_session: false } } : { status: 401, json: { error: { code: 'AUTH_REQUIRED' } } });
        if (routeName === '/auth/admin-options') return route.fulfill({ status: 403, json: { error: { code: 'DEVICE_REQUIRED' } } });
        if (routeName === '/users') return route.fulfill({ json: [user] });
        if (routeName === '/devices') return route.fulfill({ json: [] });
        if (routeName === '/shifts/current') return route.fulfill({ json: { shift: null } });
        if (routeName === '/manager/dashboard') return route.fulfill({ json: { observed_at: '2026-11-03T00:00:00Z', timezone: 'Europe/Moscow', state: 'uninitialized', cash_policy_status: 'unverified', production_blocked: false, responsible: null, initial_opening: null, pending_handover: null } });
        if (routeName === '/shifts' || routeName === '/audit-events') return route.fulfill({ json: { items: [], next_cursor: '' } });
        throw new Error('Unexpected route: ' + routeName);
      }
      assert.ok(url.pathname === '/' || /^\/assets\/[A-Za-z0-9._-]+$/.test(url.pathname));
      const name = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      return route.fulfill({ body: await readFile(path.join(dist, name)), contentType: name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' });
    });
    return result;
  }
  const anonymous = await context();
  const page = await anonymous.newPage();
  await page.goto(origin);
  const toggle = page.getByRole('button', { name: 'Анимации интерфейса', exact: true });
  await toggle.waitFor();
  assert.equal(await toggle.getAttribute('aria-pressed'), 'true');
  await page.getByRole('button', { name: 'Управляющий · пароль', exact: true }).click();
  await page.waitForFunction(() => window.interfaceAnimations.some(item => item.tag === 'FORM'));
  await page.getByLabel('Логин управляющего', { exact: true }).fill('сохранённый-ввод');
  await toggle.click();
  assert.equal(await page.locator('html').getAttribute('data-motion'), 'off');
  assert.equal(await page.getByLabel('Логин управляющего', { exact: true }).inputValue(), 'сохранённый-ввод');
  assert.ok(await page.evaluate(() => window.interfaceAnimations.every(item => item.animation.playState === 'idle')));
  await page.reload();
  assert.equal(await toggle.getAttribute('aria-pressed'), 'false', 'opt-out survives reload');
  await toggle.click();
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.waitForFunction(() => document.documentElement.dataset.motion === 'off');
  assert.equal(await toggle.isDisabled(), true, 'system preference takes precedence');
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.waitForFunction(() => document.documentElement.dataset.motion === 'on');

  const managerContext = await context(true);
  const manager = await managerContext.newPage();
  await manager.goto(origin);
  await manager.getByRole('heading', { level: 1, name: user.display_name, exact: true }).waitFor();
  await manager.getByRole('link', { name: 'Обзор клуба', exact: true }).waitFor();
  await manager.evaluate(() => { window.location.hash = '#club-overview'; });
  await manager.waitForFunction(() => window.interfaceAnimations.some(item => item.id === 'club-overview'));
  await manager.evaluate(() => { window.location.hash = '#shift-history'; });
  await manager.waitForFunction(() => window.interfaceAnimations.some(item => item.id === 'shift-history'));
  assert.equal(await manager.evaluate(() => window.interfaceAnimations.filter(item => item.id === 'club-overview').at(-1).animation.playState), 'idle', 'interrupted navigation cancels the previous animation');
  assert.equal(await manager.getByRole('link', { name: 'История', exact: true }).getAttribute('aria-current'), 'location');
  await manager.getByRole('button', { name: 'Анимации интерфейса', exact: true }).click();
  const before = await manager.evaluate(() => window.interfaceAnimations.length);
  await manager.getByRole('link', { name: 'Обзор клуба', exact: true }).click();
  assert.equal(await manager.evaluate(() => window.interfaceAnimations.length), before, 'navigation works without creating animations');
  await manager.setViewportSize({ width: 320, height: 850 });
  assert.equal(await manager.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false);

  const blockedStorage = await context();
  await blockedStorage.addInitScript(() => {
    Storage.prototype.getItem = () => { throw new Error('storage blocked'); };
    Storage.prototype.setItem = () => { throw new Error('storage blocked'); };
  });
  const restricted = await blockedStorage.newPage(); await restricted.goto(origin);
  await restricted.getByRole('button', { name: 'Анимации интерфейса', exact: true }).click();
  assert.equal(await restricted.locator('html').getAttribute('data-motion'), 'off');
  log('Motion checks passed: form transitions, interrupted navigation, persistent opt-out, system reduction, blocked storage and phone layout (mock API only).');
} finally { await browser.close(); }
