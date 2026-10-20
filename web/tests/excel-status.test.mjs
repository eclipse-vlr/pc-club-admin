import test from 'node:test';
import assert from 'node:assert/strict';
import { excelDeliveryView, excelSuccessTime, safeExcelURL } from '../src/excel-status.ts';

const sent = { requested_version: 7, synced_version: 7, last_error_code: '', last_success_at: '2026-10-20T01:01:35Z', timezone: 'Europe/Moscow' };
test('a previous successful delivery cannot confirm new saved changes or an error', () => {
  assert.equal(excelDeliveryView(sent, 7, true, false).sent, true);
  for (const state of [{ ...sent, requested_version: 8 }, { ...sent, synced_version: 6 }, { ...sent, last_error_code: 'EXCEL_ROW_EDITED' }, { ...sent, last_success_at: null }, { ...sent, last_success_at: 'invalid' }]) {
    assert.equal(excelDeliveryView(state, 7, true, false).sent, false);
  }
  assert.equal(excelDeliveryView(sent, 8, true, false).tone, 'is-pending');
  assert.equal(excelDeliveryView(sent, 7, true, true).tone, 'is-warning');
  assert.equal(excelDeliveryView(sent, 7, false, false).tone, 'is-disabled');
  assert.equal(excelDeliveryView(null, 7, true, false).tone, 'is-pending');
});
test('provider details are never displayed as a fallback error', () => {
  const state = { ...sent, last_error_code: 'secret-provider-message' };
  assert.ok(!excelDeliveryView(state, 7, true, false).message.includes('secret-provider-message'));
});
test('success time uses the club timezone, not the browser timezone', () => {
  assert.match(excelSuccessTime(sent.last_success_at, 'Europe/Moscow'), /04:01:35/);
  assert.match(excelSuccessTime(sent.last_success_at, 'UTC'), /01:01:35/);
  assert.equal(excelSuccessTime(null, 'Europe/Moscow'), null);
  assert.equal(excelSuccessTime('invalid', 'Europe/Moscow'), null);
  assert.equal(excelSuccessTime(sent.last_success_at, 'invalid/timezone'), null);
  assert.equal(excelSuccessTime(sent.last_success_at, ''), null);
});
test('only the canonical fixed-host Drive file link is rendered', () => {
  const url = 'https://drive.google.com/file/d/synthetic-test-file/view';
  assert.equal(safeExcelURL(url), url);
  for (const value of [undefined, url + '\n', ' ' + url, 'javascript:alert(1)', '//drive.google.com/file/d/synthetic-test-file/view', url + '?next=elsewhere', url.replace('drive.google.com', 'drive.google.com.evil.test'), url.replace('https:', 'http:'), url.replace('synthetic-test-file', '../private')]) {
    assert.equal(safeExcelURL(value), null);
  }
});
