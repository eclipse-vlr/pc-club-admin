import { parseExactJSON, stringifyExact } from './money.ts';
export type User = { id: string; display_name: string; manager_login: string; roles: string[]; active: boolean; version: number };
export type Identity = { user: User; device_trusted: boolean; pin_session: boolean };
export class APIError extends Error {
  code: string;
  constructor(code: string, message: string) { super(message); this.code = code; }
}
export async function api<T>(path: string, method = 'GET', body?: unknown, actor = '', idempotencyKey?: string): Promise<T> {
  const controller = new AbortController();
  // One deadline covers CSRF, submission and reading the response body.
  const deadline = setTimeout(() => controller.abort(), 20_000);
  const signal = controller.signal;
  try {
    const headers: Record<string, string> = {};
    if (method !== 'GET') {
      const response = await fetch('/api/v1/auth/csrf', { cache: 'no-store', signal });
      if (!response.ok) throw new APIError('SERVICE_UNAVAILABLE', 'Сервер недоступен');
      const token = await response.json() as { csrf_token: string };
      headers['X-CSRF-Token'] = token.csrf_token;
      headers['X-Actor-ID'] = actor;
      headers['Content-Type'] = 'application/json';
      if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey;
    }
    const response = await fetch('/api/v1' + path, { method, headers, body: body === undefined ? undefined : stringifyExact(body), cache: 'no-store', signal });
    if (response.status === 204) return undefined as T;
    const result: unknown = parseExactJSON(await response.text());
    if (!response.ok) {
      const error = result as { error?: { code?: string; message?: string } };
      throw new APIError(error.error?.code ?? 'REQUEST_FAILED', error.error?.message ?? 'Действие не выполнено');
    }
    return result as T;
  } catch (error) {
    if (signal.aborted) throw new APIError('REQUEST_TIMEOUT', 'Сервер не ответил вовремя. Результат действия неизвестен — перечитайте данные перед повтором.');
    throw error;
  } finally {
    clearTimeout(deadline);
  }
}
