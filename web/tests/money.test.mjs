import test from 'node:test';
import assert from 'node:assert/strict';
import { parseRubles, formatRubles, parseExactJSON, stringifyExact } from '../src/money.ts';
test('rubles remain exact across the complete int64 range', () => {
  assert.equal(parseRubles('123,45'), 12345n);
  assert.equal(parseRubles('92233720368547758.07'), 9223372036854775807n);
  assert.equal(parseRubles('-92233720368547758.08'), -9223372036854775808n);
  assert.equal(formatRubles('9223372036854775807'), '92233720368547758,07 ₽');
  for (const value of ['', '1.001', '1e3', '92233720368547758.08']) assert.throws(() => parseRubles(value));
});
test('integer kopecks survive JSON without Number conversion or text corruption', () => {
  const json = stringifyExact({ version: 3, amount_kopecks: 9223372036854775807n, note: 'Текст "amount_kopecks": 123' });
  assert.ok(json.includes('"amount_kopecks":9223372036854775807'));
  assert.deepEqual(parseExactJSON(json), { version: 3, amount_kopecks: '9223372036854775807', note: 'Текст "amount_kopecks": 123' });
  assert.deepEqual(parseExactJSON('{"amount_kopecks":null,"version":2,"nested":{"difference_kopecks":-1}}'), { amount_kopecks: null, version: 2, nested: { difference_kopecks: '-1' } });
  assert.throws(() => parseExactJSON('{"amount_kopecks":1.1}'));
});
