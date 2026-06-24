import type { AuthStatus } from './types';

const API_BASE = import.meta.env.VITE_API_BASE;

async function handle<T>(res: Response): Promise<T> {
  if (res.status === 401) {
    window.dispatchEvent(new CustomEvent('auth:expired'));
    throw new Error('unauthorized');
  }
  if (!res.ok) {
    throw new Error(`${res.status} ${res.statusText}`);
  }
  return (await res.json()) as T;
}

export async function login(username: string, password: string): Promise<boolean> {
  const res = await fetch('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'include',
    body: JSON.stringify({ username, password }),
  });
  if (res.status === 401) return false;
  if (!res.ok) throw new Error(`${res.status}`);
  return true;
}

export async function logout(): Promise<void> {
  await fetch('/api/logout', { method: 'POST', credentials: 'include' });
}

export async function authStatus(): Promise<AuthStatus> {
  const res = await fetch('/api/auth/status', { credentials: 'include' });
  if (!res.ok) return { authenticated: false };
  return res.json();
}

export async function pathsList(): Promise<{
  items: Array<{
    name: string;
    ready: boolean;
    readyTime?: string;
    readers?: Array<{ type: string; id: string }>;
  }>;
}> {
  const res = await fetch(`${API_BASE}/v3/paths/list`, { credentials: 'include' });
  return handle(res);
}
