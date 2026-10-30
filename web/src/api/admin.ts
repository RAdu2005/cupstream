import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useMe } from './auth';
import { api, ApiError } from './client';
import type { AdminOverview, Me } from './types.gen';

const overviewKey = ['admin'] as const;

/** Whether the signed-in user is an admin (CUPSTREAM_ADMINS on the server). */
export function useIsAdmin() {
  const me = useMe();
  const q = useQuery({
    queryKey: ['me', 'admin', me.data?.id],
    queryFn: async () => (await api<Me>('/api/me')).admin ?? false,
    enabled: !!me.data,
    staleTime: 5 * 60_000,
  });
  return !!me.data && q.data === true;
}

/** Every live stream, what it costs, and the banned users; refreshed every 2 seconds. */
export function useAdminOverview() {
  return useQuery({
    queryKey: overviewKey,
    queryFn: () => api<AdminOverview>('/api/admin'),
    refetchInterval: 2000,
    retry: (n, err) => !(err instanceof ApiError && (err.status === 401 || err.status === 403)) && n < 2,
  });
}

function useAdminAction<T>(fn: (arg: T) => Promise<void>) {
  const qc = useQueryClient();
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: overviewKey }) });
}

export function useAdminEndStream() {
  return useAdminAction((id: string) => api<void>(`/api/admin/streams/${id}/end`, { method: 'POST' }));
}

export function useBan() {
  return useAdminAction((handle: string) => api<void>(`/api/admin/bans/${encodeURIComponent(handle)}`, { method: 'PUT' }));
}

export function useUnban() {
  return useAdminAction((handle: string) =>
    api<void>(`/api/admin/bans/${encodeURIComponent(handle)}`, { method: 'DELETE' }),
  );
}
