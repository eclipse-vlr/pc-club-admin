import { parseExactJSON, stringifyExact } from './money';
export type User = { id: string; display_name: string; manager_login: string; roles: string[]; active: boolean; version: number };
export type Identity = { user: User; device_trusted: boolean; pin_session: boolean };
export class APIError extends Error {
  constructor(public code: string, message: string) { super(message); }
}
export async function api<T>(path: string, method = 'GET', body?: unknown, actor = '', idempotencyKey?: string): Promise<T> {
  const headers: Record<string, string> = {};
  if (method !== 'GET') {
    const response = await fetch('/api/v1/auth/csrf', { cache: 'no-store' });
    if (!response.ok) throw new APIError('SERVICE_UNAVAILABLE', 'Сервер недоступен');
    const token = await response.json() as { csrf_token: string };
    headers['X-CSRF-Token'] = token.csrf_token;
    headers['X-Actor-ID'] = actor;
    headers['Content-Type'] = 'application/json';
    if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey;
  }
  const response = await fetch('/api/v1' + path, { method, headers, body: body === undefined ? undefined : stringifyExact(body), cache: 'no-store' });
  if (response.status === 204) return undefined as T;
  const result: unknown = parseExactJSON(await response.text());
  if (!response.ok) {
    const error = result as { error?: { code?: string; message?: string } };
    throw new APIError(error.error?.code ?? 'REQUEST_FAILED', error.error?.message ?? 'Действие не выполнено');
  }
  return result as T;
}
