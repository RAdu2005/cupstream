import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { api, ApiError } from './client';
import type { AuthProviders, DevLoginRequest, HandleCheck, Me, PendingSignUp, SignUpRequest, User } from './types.gen';

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

/** A new user's sign-in, waiting for them to choose a handle; null when none is. */
export function usePendingSignUp() {
  return useQuery({
    queryKey: ['auth', 'signup'],
    queryFn: async (): Promise<PendingSignUp | null> => {
      try {
        return await api<PendingSignUp>('/api/auth/signup');
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) return null;
        throw err;
      }
    },
    staleTime: Infinity,
  });
}

export function useSignUp() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: SignUpRequest) => api<Me>('/api/auth/signup', { method: 'POST', json: req }),
    onSuccess: (me) => {
      qc.setQueryData(meKey, me.user);
      qc.removeQueries({ queryKey: ['auth', 'signup'] });
    },
  });
}

export function useCancelSignUp() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>('/api/auth/signup', { method: 'DELETE' }),
    onSuccess: () => qc.removeQueries({ queryKey: ['auth', 'signup'] }),
  });
}

/**
 * Whether the signed-in user, or a new one, may take a handle. It is checked
 * once typing pauses; nothing is checked for an empty handle or `skip`.
 */
export function useHandleCheck(handle: string, skip = false) {
  const [debounced, setDebounced] = useState(handle);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(handle), 300);
    return () => clearTimeout(t);
  }, [handle]);
  const h = debounced.trim().toLowerCase();
  const check = useQuery({
    queryKey: ['auth', 'handle', h],
    queryFn: () => api<HandleCheck>(`/api/auth/handle?handle=${encodeURIComponent(h)}`),
    enabled: h !== '' && !skip,
    staleTime: 10_000,
  });
  return debounced === handle && h !== '' && !skip ? check.data : undefined;
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
