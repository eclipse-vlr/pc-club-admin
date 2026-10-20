import test from 'node:test';
import assert from 'node:assert/strict';
import { api, APIError } from '../src/api.ts';
import { actionFeedback } from '../src/action-feedback.ts';

test('a rejected request and an unknown outcome give different recovery instructions', () => {
  const rejected = actionFeedback(new APIError('RECEIPT_MISMATCH', 'Пересчёт не совпадает', 422));
  assert.match(rejected.instruction, /отозвать передачу/);
  assert.doesNotMatch(rejected.instruction, /могло сохраниться/);
  for (const code of ['NETWORK_ERROR', 'REQUEST_TIMEOUT', 'INVALID_RESPONSE', 'SERVICE_UNAVAILABLE']) {
    assert.match(actionFeedback(new APIError(code, 'Связь прервалась')).instruction, /могло сохраниться/);
  }
  const saved = actionFeedback(new APIError('AUTH_REQUIRED', 'Войдите', 401), true);
  assert.match(saved.message, /Действие сохранено/);
  assert.match(saved.instruction, /Повторно отправлять.*не нужно/);
});

test('network failures are localized and never retried automatically', async t => {
  let writes = 0;
  t.mock.method(globalThis, 'fetch', async (_url, options) => {
    if (!options.method) return new globalThis.Response(JSON.stringify({ csrf_token: 'synthetic' }));
    writes++;
    throw new TypeError('Failed to fetch');
  });
  await assert.rejects(api('/handovers/synthetic/accept', 'POST', {}, 'synthetic', 'synthetic-stable-key'), cause => {
    assert.equal(cause.code, 'NETWORK_ERROR');
    assert.match(cause.message, /Связь.*прервалась/);
    assert.doesNotMatch(cause.message, /Failed to fetch/);
    return true;
  });
  assert.equal(writes, 1);
});

test('a malformed success response cannot be reported as a successful mutation', async t => {
  t.mock.method(globalThis, 'fetch', async (_url, options) => !options.method
    ? new globalThis.Response(JSON.stringify({ csrf_token: 'synthetic' }))
    : new globalThis.Response('<html>gateway response</html>', { status: 200 }));
  await assert.rejects(api('/handovers/synthetic/accept', 'POST', {}), cause => cause.code === 'INVALID_RESPONSE');
});

test('a malformed CSRF response stops the action before submission', async t => {
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async () => { calls++; return new globalThis.Response('<html>gateway</html>'); });
  await assert.rejects(api('/handovers/synthetic/accept', 'POST', {}), cause => {
    assert.equal(cause.code, 'INVALID_RESPONSE');
    assert.match(cause.message, /ответ сервера не распознан/);
    return true;
  });
  assert.equal(calls, 1);
});

test('the HTTP rejection status is retained for safe recovery guidance', async t => {
  t.mock.method(globalThis, 'fetch', async () => new globalThis.Response(JSON.stringify({ error: { code: 'HANDOVER_CHANGED', message: 'Передача изменена' } }), { status: 409 }));
  await assert.rejects(api('/handovers/synthetic'), cause => {
    assert.equal(cause.status, 409);
    assert.equal(cause.code, 'HANDOVER_CHANGED');
    assert.doesNotMatch(actionFeedback(cause).instruction, /Результат действия неизвестен/);
    return true;
  });
});
