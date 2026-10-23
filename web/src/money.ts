const MIN = -(1n << 63n);
const MAX = (1n << 63n) - 1n;
export class MoneyInputError extends Error {
  field: string;
  constructor(message: string, field = '') { super(message); this.name = 'MoneyInputError'; this.field = field; }
}
export function parseRubles(raw: string): bigint {
  const text = raw.trim().replace(',', '.');
  if (!/^-?\d+(?:\.\d{1,2})?$/.test(text)) throw new MoneyInputError('Введите рубли точно, максимум два знака после запятой. Пустое поле не равно нулю.');
  const negative = text.startsWith('-');
  const [whole, fraction = ''] = (negative ? text.slice(1) : text).split('.');
  const amount = (BigInt(whole) * 100n + BigInt(fraction.padEnd(2, '0'))) * (negative ? -1n : 1n);
  if (amount < MIN || amount > MAX) throw new MoneyInputError('Сумма слишком велика. Проверьте введённое значение.');
  return amount;
}
export function formatRubles(raw: string | null): string {
  if (raw === null) return 'Не введено';
  const value = BigInt(raw); const absolute = value < 0n ? -value : value;
  return `${value < 0n ? '−' : ''}${absolute / 100n},${String(absolute % 100n).padStart(2, '0')} ₽`;
}
export function rublesInput(raw: string | null): string { return raw === null ? '' : formatRubles(raw).replace(' ₽', '').replace('−', '-'); }

// JSON wire format stays integer kopecks. BigInt never passes through Number.
export function stringifyExact(value: unknown): string {
  if (typeof value === 'bigint') {
    if (value < MIN || value > MAX) throw new Error('Сумма выходит за границы int64');
    return value.toString();
  }
  if (Array.isArray(value)) return '[' + value.map(stringifyExact).join(',') + ']';
  if (value !== null && typeof value === 'object') return '{' + Object.entries(value).filter(([, item]) => item !== undefined).map(([key, item]) => JSON.stringify(key) + ':' + stringifyExact(item)).join(',') + '}';
  if (typeof value === 'number' && !Number.isSafeInteger(value)) throw new Error('Неточное число запрещено');
  return JSON.stringify(value) ?? 'null';
}
// Tokenize strings as complete units; numbers appearing in user text are untouched.
// Integer *_kopecks values are kept as decimal strings after decoding, including
// values outside JavaScript's safe integer range.
export function parseExactJSON(text: string): unknown {
  let money = false;
  const transformed = text.replace(/"(?:\\.|[^"\\])*"|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/g, (token: string, offset: number) => {
    if (token.startsWith('"')) {
      const isKey = /^\s*:/.test(text.slice(offset + token.length));
      money = isKey && String(JSON.parse(token)).endsWith('_kopecks');
      return token;
    }
    if (!money) return token;
    money = false;
    if (!/^-?\d+$/.test(token)) throw new Error('Денежное поле API должно содержать целые копейки');
    return JSON.stringify(token);
  });
  return JSON.parse(transformed) as unknown;
}
