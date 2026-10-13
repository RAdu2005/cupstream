import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, ApiError } from './client';
import type { AuthProviders, DevLoginRequest, Me, User } from './types.gen';

const meKey = ['me'] as const;

/** The signed-in user, or null when signed out. */
export function useMe() {
  return useQuery({
    queryKey: meKey,
    queryFn: async (): Promise<User | null> => {
      try {
        return (await api<Me>('/api/me')).user;
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) return null;
        throw err;
      }
    },
    staleTime: 60_000,
  });
}

export function useAuthProviders() {
  return useQuery({
    queryKey: ['auth', 'providers'],
    queryFn: () => api<AuthProviders>('/api/auth/providers'),
    staleTime: Infinity,
  });
}

export function useDevLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: DevLoginRequest) => api<Me>('/api/auth/dev/login', { method: 'POST', json: req }),
    onSuccess: (me) => qc.setQueryData(meKey, me.user),
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>('/api/auth/logout', { method: 'POST' }),
    onSuccess: () => {
      qc.setQueryData(meKey, null);
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== 'me' && q.queryKey[0] !== 'auth' });
    },
  });
}
