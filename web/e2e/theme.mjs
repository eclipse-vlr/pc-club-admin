/* global document, Storage, window, getComputedStyle */
// Real Chromium with intercepted API and static assets; no credentials or Google.
import assert from 'node:assert/strict';
import { readFile, mkdir } from 'node:fs/promises';
import { fileURLToPath, URL } from 'node:url';
import path from 'node:path';
import process from 'node:process';
import { log } from 'node:console';
import { chromium } from 'playwright-core';

const origin = 'http://127.0.0.1:54321';
const dist = fileURLToPath(new URL('../dist/', import.meta.url));
const browser = await chromium.launch({ executablePath: process.env.PC_CLUB_CHROMIUM || '/usr/bin/chromium', args: ['--no-sandbox'] });
const errors = [];
try {
  async function context(options = {}) {
    const result = await browser.newContext(options);
    result.on('page', page => page.on('pageerror', error => errors.push(error.message)));
    await result.route('**/*', async route => {
      const url = new URL(route.request().url());
      assert.equal(url.origin, origin, 'no external requests');
      if (url.pathname.startsWith('/api/')) {
        if (url.pathname === '/api/v1/status') return route.fulfill({ json: { environment: 'test', excel_sync_enabled: false, timezone: 'Europe/Moscow' } });
        return route.fulfill({ status: url.pathname.endsWith('/me') ? 401 : 403, json: { error: { code: url.pathname.endsWith('/me') ? 'AUTH_REQUIRED' : 'DEVICE_REQUIRED' } } });
      }
      assert.ok(url.pathname === '/' || /^\/assets\/[A-Za-z0-9._-]+$/.test(url.pathname));
      const name = url.pathname === '/' ? 'index.html' : url.pathname.slice(1);
      return route.fulfill({ body: await readFile(path.join(dist, name)), contentType: name.endsWith('.js') ? 'text/javascript' : name.endsWith('.css') ? 'text/css' : 'text/html' });
    });
    return result;
  }
  const regular = await context({ viewport: { width: 1366, height: 900 }, reducedMotion: 'reduce' });
  const page = await regular.newPage();
  await page.goto(origin);
  const toggle = page.getByRole('button', { name: 'Тёмная тема', exact: true });
  await toggle.waitFor();
  assert.equal(await toggle.getAttribute('aria-pressed'), 'true');
  assert.equal(await page.evaluate(() => getComputedStyle(document.documentElement).colorScheme), 'dark');
  await toggle.focus(); await page.keyboard.press('Enter');
  assert.equal(await toggle.getAttribute('aria-pressed'), 'false');
  await page.reload(); await toggle.waitFor();
  assert.equal(await toggle.getAttribute('aria-pressed'), 'false', 'explicit choice survives reload');
  assert.equal(await page.evaluate(() => getComputedStyle(document.documentElement).colorScheme), 'light');
  await page.getByRole('button', { name: 'Управляющий · пароль', exact: true }).click();
  for (const theme of ['light', 'dark']) {
    if (theme === 'dark') await toggle.click();
    const contrastFailures = await page.evaluate(() => {
      function channels(color) { return color.match(/[\d.]+/g).map(Number); }
      function luminance(color) {
        const values = channels(color).slice(0, 3).map(channel => {
          const value = channel / 255;
          return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
        });
        return values[0] * 0.2126 + values[1] * 0.7152 + values[2] * 0.0722;
      }
      const failures = [];
      for (const node of document.querySelectorAll('button:not(:disabled), a, label, .page-description, .notice > p')) {
        if (!node.getClientRects().length) continue;
        let background = node;
        while (background.parentElement && channels(getComputedStyle(background).backgroundColor)[3] === 0) background = background.parentElement;
        const style = getComputedStyle(node);
        const foregroundL = luminance(style.color);
        const backgroundL = luminance(getComputedStyle(background).backgroundColor);
        const ratio = (Math.max(foregroundL, backgroundL) + 0.05) / (Math.min(foregroundL, backgroundL) + 0.05);
        if (ratio < 4.5) failures.push({ text: node.textContent.trim().slice(0, 40), ratio });
      }
      return failures;
    });
    assert.deepEqual(contrastFailures, [], theme + ' login controls and notices meet 4.5:1 text contrast');
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
    assert.equal(overflow, false);
    if (process.env.PC_CLUB_SCREENSHOT_DIR) {
      await mkdir(process.env.PC_CLUB_SCREENSHOT_DIR, { recursive: true });
      await page.screenshot({ path: path.join(process.env.PC_CLUB_SCREENSHOT_DIR, `theme-${theme}-desktop.png`), fullPage: true });
    }
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false, theme + ' fits phone width');
    if (process.env.PC_CLUB_SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.PC_CLUB_SCREENSHOT_DIR, `theme-${theme}-mobile.png`), fullPage: true });
    await page.setViewportSize({ width: 1366, height: 900 });
  }
  const restricted = await context();
  await restricted.addInitScript(() => {
    Storage.prototype.getItem = () => { throw new Error('storage blocked'); };
    Storage.prototype.setItem = () => { throw new Error('storage blocked'); };
  });
  const restrictedPage = await restricted.newPage(); await restrictedPage.goto(origin);
  await restrictedPage.getByRole('button', { name: 'Тёмная тема', exact: true }).click();
  assert.equal(await restrictedPage.locator('html').getAttribute('data-theme'), 'light', 'theme works without storage');
  assert.deepEqual(errors, []);
  log('Theme browser checks passed: dark default, keyboard toggle, saved choice, blocked storage, desktop and phone.');
} finally { await browser.close(); }
