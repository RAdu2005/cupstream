import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, ApiError } from './client';
import type {
  Account,
  AuthProvider,
  Guild,
  Me,
  NewStreamKey,
  PendingMerge,
  Session,
  Settings,
  StreamKeyInfo,
  UpdateProfileRequest,
} from './types.gen';

export const accountKey = ['account'] as const;

export function useAccount() {
  return useQuery({ queryKey: accountKey, queryFn: () => api<Account>('/api/account') });
}

/** Invalidates everything that shows the user's own data. */
function useRefreshAccount() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: accountKey });
    void qc.invalidateQueries({ queryKey: ['me'] });
  };
}

export function useUpdateProfile() {
  const refresh = useRefreshAccount();
  return useMutation({
    mutationFn: (req: UpdateProfileRequest) => api<Me>('/api/account', { method: 'PATCH', json: req }),
    onSuccess: refresh,
  });
}

export function useUpdateSettings() {
  const refresh = useRefreshAccount();
  return useMutation({
    mutationFn: (req: Settings) => api<Settings>('/api/account/settings', { method: 'PATCH', json: req }),
    onSuccess: refresh,
  });
}

export function useUnlink() {
  const refresh = useRefreshAccount();
  return useMutation({
    mutationFn: (provider: AuthProvider) => api<void>(`/api/account/identities/${provider}`, { method: 'DELETE' }),
    onSuccess: refresh,
  });
}

export function useSessions() {
  return useQuery({ queryKey: ['account', 'sessions'], queryFn: () => api<Session[]>('/api/account/sessions') });
}

export function useRevokeSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/api/account/sessions/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['account', 'sessions'] }),
  });
}

export function useGuilds(enabled: boolean) {
  return useQuery({ queryKey: ['account', 'guilds'], queryFn: () => api<Guild[]>('/api/account/guilds'), enabled });
}

export function useUpdateGuild() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, suggestionsEnabled }: { id: string; suggestionsEnabled: boolean }) =>
      api(`/api/account/guilds/${id}`, { method: 'PATCH', json: { suggestionsEnabled } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['account', 'guilds'] }),
  });
}

export function usePendingMerge(enabled: boolean) {
  return useQuery({
    queryKey: ['account', 'merge'],
    enabled,
    retry: false,
    queryFn: async () => {
      try {
        return await api<PendingMerge>('/api/account/merge');
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) return null;
        throw err;
      }
    },
  });
}

export function useResolveMerge() {
  const qc = useQueryClient();
  const refresh = useRefreshAccount();
  return useMutation({
    mutationFn: (confirm: boolean) => api<void>('/api/account/merge', { method: confirm ? 'POST' : 'DELETE' }),
    onSuccess: () => {
      qc.setQueryData(['account', 'merge'], null);
      refresh();
    },
  });
}

export function useStreamKey() {
  return useQuery({ queryKey: ['account', 'stream-key'], queryFn: () => api<StreamKeyInfo>('/api/account/stream-key') });
}

export function useRegenerateStreamKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<NewStreamKey>('/api/account/stream-key', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['account', 'stream-key'] }),
  });
}
