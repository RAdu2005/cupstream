import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './client';
import type { AllowedEntry, Room, Visibility } from './types.gen';

export const roomKey = (slug: string) => ['room', slug] as const;

/** A room and its live streams, polled until realtime events exist. */
export function useRoom(slug: string) {
  return useQuery({
    queryKey: roomKey(slug),
    queryFn: () => api<Room>(`/api/rooms/${slug}`),
    refetchInterval: 3000,
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

export function useAllowed(slug: string) {
  return useQuery({
    queryKey: ['room', slug, 'allowed'],
    queryFn: () => api<AllowedEntry[]>(`/api/rooms/${slug}/allowed`),
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
