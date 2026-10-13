import type { ErrorResponse } from './types.gen';

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

/** Calls the CupStream API and returns the decoded JSON body (undefined for 204). */
export async function api<T>(path: string, init: RequestInit & { json?: unknown } = {}): Promise<T> {
  const { json, ...rest } = init;
  const headers = new Headers(rest.headers);
  if (json !== undefined) headers.set('Content-Type', 'application/json');

  const res = await fetch(path, {
    ...rest,
    headers,
    body: json !== undefined ? JSON.stringify(json) : rest.body,
    credentials: 'same-origin',
  });

  if (!res.ok) {
    let message = res.statusText;
    try {
      message = ((await res.json()) as ErrorResponse).error || message;
    } catch {
      // not a JSON error body
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}
