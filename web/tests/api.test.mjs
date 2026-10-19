import test from 'node:test';
import assert from 'node:assert/strict';
import { api, APIError } from '../src/api.ts';

test('a stalled CSRF request times out without submitting a mutation', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const calls = [];
  t.mock.method(globalThis, 'fetch', (_url, options) => new Promise((_resolve, reject) => {
    calls.push(options);
    options?.signal?.addEventListener('abort', () => reject(new globalThis.DOMException('Aborted', 'AbortError')));
  }));
  const pending = api('/shifts/example/cash', 'PATCH', {}, 'actor', 'test-key');
  const rejected = assert.rejects(pending, error => error instanceof APIError && error.code === 'REQUEST_TIMEOUT');
  assert.ok(calls[0].signal instanceof globalThis.AbortSignal, 'requests need a bounded lifetime');
  t.mock.timers.tick(20_000);
  await rejected;
  assert.equal(calls.length, 1, 'no mutation after CSRF timeout');
});

test('a stalled mutation times out without an automatic monetary retry', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const calls = [];
  let mutationStarted;
  const started = new Promise(resolve => { mutationStarted = resolve; });
  t.mock.method(globalThis, 'fetch', (url, options) => {
    calls.push({ url, options });
    if (url.endsWith('/csrf')) return Promise.resolve(globalThis.Response.json({ csrf_token: 'csrf' }));
    mutationStarted();
    return new Promise((_resolve, reject) => {
      options?.signal?.addEventListener('abort', () => reject(new globalThis.DOMException('Aborted', 'AbortError')));
    });
  });
  const pending = api('/shifts/example/cash/collections', 'POST', { amount_kopecks: 1n }, 'actor', 'original-key');
  const rejected = assert.rejects(pending, error => error instanceof APIError && error.code === 'REQUEST_TIMEOUT');
  await started;
  assert.ok(calls[1].options.signal instanceof globalThis.AbortSignal);
  assert.equal(calls[0].options.signal, calls[1].options.signal, 'CSRF and mutation share one deadline');
  t.mock.timers.tick(20_000);
  await rejected;
  assert.equal(calls.length, 2, 'unknown outcome must not trigger another POST');
  assert.equal(calls[1].options.headers['Idempotency-Key'], 'original-key');
});

test('money transport preserves int64, identity, key and clears the deadline after success', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let sent;
  t.mock.method(globalThis, 'fetch', (url, options) => {
    if (url.endsWith('/csrf')) return Promise.resolve(globalThis.Response.json({ csrf_token: 'csrf' }));
    sent = options;
    return Promise.resolve(new globalThis.Response('{"amount_kopecks":9223372036854775807}'));
  });
  assert.deepEqual(await api('/cash', 'PATCH', { amount_kopecks: 9223372036854775807n }, 'actor', 'key'), { amount_kopecks: '9223372036854775807' });
  assert.equal(sent.body, '{"amount_kopecks":9223372036854775807}');
  assert.equal(sent.headers['X-Actor-ID'], 'actor');
  assert.equal(sent.headers['Idempotency-Key'], 'key');
  t.mock.timers.tick(20_000);
  assert.equal(sent.signal.aborted, false);
});
