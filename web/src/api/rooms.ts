import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './client';
import type { AllowedEntry, BanEntry, Room, Visibility } from './types.gen';

export const roomKey = (slug: string) => ['room', slug] as const;

/**
 * A room and its live streams. The streams are kept current by realtime
 * events (useRoomRealtime), which also refetch the room when needed.
 */
export function useRoom(slug: string) {
  return useQuery({
    queryKey: roomKey(slug),
    queryFn: () => api<Room>(`/api/rooms/${slug}`),
    retry: false,
  });
}

export function useUpdateRoom(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (visibility: Visibility) => api(`/api/rooms/${slug}`, { method: 'PATCH', json: { visibility } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: roomKey(slug) }),
  });
}

export function useAllowed(slug: string, enabled = true) {
  return useQuery({
    queryKey: ['room', slug, 'allowed'],
    queryFn: () => api<AllowedEntry[]>(`/api/rooms/${slug}/allowed`),
    enabled,
  });
}

export function useSetAllowed(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ handle, canStream }: { handle: string; canStream: boolean }) =>
      api<AllowedEntry>(`/api/rooms/${slug}/allowed/${encodeURIComponent(handle)}`, {
        method: 'PUT',
        json: { canStream },
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['room', slug, 'allowed'] }),
  });
}

export function useRemoveAllowed(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (handle: string) =>
      api<void>(`/api/rooms/${slug}/allowed/${encodeURIComponent(handle)}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['room', slug, 'allowed'] }),
  });
}

export function useEndStream(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/api/streams/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: roomKey(slug) }),
  });
}

/** The owner ends someone's sessions and temporary access to the room. */
export function useKick(slug: string) {
  return useMutation({
    mutationFn: (handle: string) =>
      api<void>(`/api/rooms/${slug}/kick/${encodeURIComponent(handle)}`, { method: 'POST' }),
  });
}

export function useBans(slug: string) {
  return useQuery({
    queryKey: ['room', slug, 'bans'],
    queryFn: () => api<BanEntry[]>(`/api/rooms/${slug}/bans`),
  });
}

/** Banning also kicks and removes from the allowed list. */
export function useBan(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (handle: string) =>
      api<void>(`/api/rooms/${slug}/bans/${encodeURIComponent(handle)}`, { method: 'PUT' }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['room', slug, 'bans'] });
      void qc.invalidateQueries({ queryKey: ['room', slug, 'allowed'] });
    },
  });
}

export function useUnban(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (handle: string) =>
      api<void>(`/api/rooms/${slug}/bans/${encodeURIComponent(handle)}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['room', slug, 'bans'] }),
  });
}
