import { useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useReducer, useSyncExternalStore } from 'react';
import { realtime, type ServerMessage } from '@/lib/realtime';
import { useMe } from './auth';
import { roomKey } from './rooms';
import type { ChatMessage, Presence, Room, Stream, ViewerStats } from './types.gen';

/** Chat messages are deleted after this long; open pages hide them too. */
export const CHAT_TTL_SECONDS = 24 * 60 * 60;

export type ChatError = { message: string; body?: string };

type State = {
  presence: Presence;
  chat: ChatMessage[];
  viewerStats: Record<string, ViewerStats>;
  chatError: ChatError | null;
  /** Set when the room's owner kicked this viewer. */
  kicked: boolean;
  /** The last message sent, given back if the server refuses it. */
  lastSent: { ref: string; body: string } | null;
};

type Action =
  | { type: 'message'; msg: ServerMessage }
  | { type: 'sent'; ref: string; body: string }
  | { type: 'error'; error: ChatError | null }
  | { type: 'expire'; now: number };

const initial: State = {
  presence: { members: [], anonymous: 0 },
  chat: [],
  viewerStats: {},
  chatError: null,
  kicked: false,
  lastSent: null,
};

function notExpired(now: number) {
  return (m: ChatMessage) => m.createdAt > now - CHAT_TTL_SECONDS;
}

function reduce(state: State, action: Action): State {
  switch (action.type) {
    case 'sent':
      return { ...state, chatError: null, lastSent: { ref: action.ref, body: action.body } };
    case 'error':
      return { ...state, chatError: action.error };
    case 'expire': {
      const chat = state.chat.filter(notExpired(action.now));
      return chat.length === state.chat.length ? state : { ...state, chat };
    }
  }
  const msg = action.msg;
  switch (msg.type) {
    case 'subscribed':
      return { ...state, presence: msg.presence, chat: msg.chat, kicked: false };
    case 'refused':
      return msg.code === 'kicked' ? { ...state, kicked: true } : state;
    case 'presence':
      return { ...state, presence: msg.presence };
    case 'chat':
      if (state.chat.some((m) => m.id === msg.message.id)) return state;
      return { ...state, chat: [...state.chat, msg.message] };
    case 'chatDeleted':
      return { ...state, chat: state.chat.filter((m) => m.id !== msg.id) };
    case 'viewerStats':
      return { ...state, viewerStats: { ...state.viewerStats, [msg.streamId]: msg } };
    case 'error': {
      // Only chat messages carry a ref; other failures need no answer here.
      if (!msg.ref) return state;
      const body = state.lastSent?.ref === msg.ref ? state.lastSent.body : undefined;
      return { ...state, chatError: { message: msg.error, body } };
    }
  }
  return state;
}

function upsertStream(streams: Stream[], s: Stream) {
  const i = streams.findIndex((x) => x.id === s.id);
  if (i < 0) return [...streams, s].sort((a, b) => a.startedAt - b.startedAt);
  return streams.map((x) => (x.id === s.id ? s : x));
}

/**
 * Follows a room over the tab's WebSocket: live streams are written into
 * the room query (no polling), and presence and chat are returned. After a
 * reconnect the room is fetched again, in case its settings changed meanwhile.
 */
export function useRoomRealtime(slug: string, enabled: boolean) {
  const qc = useQueryClient();
  const me = useMe();
  const userId = me.isPending ? undefined : (me.data?.id ?? '');
  const [state, dispatch] = useReducer(reduce, initial);
  const status = useSyncExternalStore(realtime.onStatus, () => realtime.status);

  useEffect(() => {
    if (userId !== undefined) realtime.setIdentity(userId);
  }, [userId]);

  useEffect(() => realtime.onSignedOut(() => void qc.invalidateQueries({ queryKey: ['me'] })), [qc]);

  useEffect(() => {
    if (!enabled || userId === undefined) return;
    let subscribed = false;
    const setStreams = (fn: (s: Stream[]) => Stream[]) =>
      qc.setQueryData<Room>(roomKey(slug), (r) => r && { ...r, streams: fn(r.streams) });

    return realtime.subscribe(slug, (msg) => {
      switch (msg.type) {
        case 'subscribed':
          setStreams(() => msg.streams);
          if (subscribed) void qc.invalidateQueries({ queryKey: roomKey(slug), exact: true });
          subscribed = true;
          break;
        case 'stream':
          setStreams((s) => upsertStream(s, msg.stream));
          break;
        case 'streamEnded':
          setStreams((s) => s.filter((x) => x.id !== msg.streamId));
          break;
        case 'refused':
        case 'roomChanged':
          // The room page shows why access ended, or the new settings.
          void qc.invalidateQueries({ queryKey: roomKey(slug), exact: true });
          break;
      }
      dispatch({ type: 'message', msg });
    });
  }, [slug, enabled, userId, qc]);

  useEffect(() => {
    const t = setInterval(() => dispatch({ type: 'expire', now: Date.now() / 1000 }), 60_000);
    return () => clearInterval(t);
  }, []);

  const sendChat = useCallback(
    (body: string) => {
      const ref = crypto.randomUUID();
      if (!realtime.send({ type: 'chat', room: slug, body, ref })) {
        dispatch({ type: 'error', error: { message: 'Not connected. Try again in a moment.', body } });
        return false;
      }
      dispatch({ type: 'sent', ref, body });
      return true;
    },
    [slug],
  );

  const deleteChat = useCallback(
    (id: string) => {
      if (!realtime.send({ type: 'deleteChat', room: slug, id, ref: crypto.randomUUID() })) {
        dispatch({ type: 'error', error: { message: 'Not connected. Try again in a moment.' } });
      }
    },
    [slug],
  );

  const chat = state.chat.filter(notExpired(Date.now() / 1000));
  return { ...state, chat, status, sendChat, deleteChat };
}
