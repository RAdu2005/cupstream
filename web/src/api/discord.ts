import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { realtime, type ServerMessage } from '@/lib/realtime';
import { useMe } from './auth';
import { api } from './client';
import type {
  DiscordChannel,
  MoveStreamRequest,
  MoveStreamResponse,
  StreamViewer,
} from './types.gen';

/** Where to go to enter a Discord room; names only for the server's members. */
export function useDiscordChannel(slug: string, enabled: boolean) {
  return useQuery({
    queryKey: ['room', slug, 'discord'],
    queryFn: () => api<DiscordChannel>(`/api/rooms/${slug}/discord`),
    enabled,
    retry: false,
  });
}

/** Listens to realtime messages about the signed-in user (not a room). */
export function useUserEvents(fn: (msg: ServerMessage) => void) {
  const me = useMe();
  const userId = me.data?.id;
  useEffect(() => {
    if (!userId) return;
    realtime.setIdentity(userId);
    return realtime.listen(fn);
  }, [userId, fn]);
}

/** A Discord room's name for people who may see it, e.g. "#General". */
export function channelLabel(c: DiscordChannel | undefined) {
  return c?.channelName ? `#${c.channelName}` : undefined;
}

/** Who watches the user's stream in a Discord room, to choose who follows it. */
export function useStreamViewers(streamId: string, enabled: boolean) {
  return useQuery({
    queryKey: ['stream', streamId, 'viewers'],
    queryFn: () => api<StreamViewer[]>(`/api/streams/${streamId}/viewers`),
    enabled,
  });
}

/** Moves the user's stream from a Discord room into their link room. */
export function useMoveStream(streamId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: MoveStreamRequest) =>
      api<MoveStreamResponse>(`/api/streams/${streamId}/move`, { method: 'POST', json: req }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['room'] }),
  });
}
