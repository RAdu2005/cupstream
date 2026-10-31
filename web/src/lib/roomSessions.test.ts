import { vi } from 'vitest';
import { ApiError } from '@/api/client';
import type { Stream } from '@/api/types.gen';
import { RoomSessions, type Want } from './roomSessions';
import type { Tracks, WhepSession } from './whep';

const alice = { id: 'u1', handle: 'alice', displayName: 'Alice' };
const stream = (id: string, state: Stream['state'] = 'live'): Stream => ({
  id,
  user: alice,
  source: 'obs',
  state,
  startedAt: 1,
  viewers: 0,
  bitrate: 0,
  lossRatio: 0,
});

type Call = { url?: string; action?: string; tracks?: Tracks; closed?: string };

/** A fake WHEP client that records what the manager asks the server. */
function fakeServer() {
  const calls: Call[] = [];
  const refuse = new Map<string, ApiError>();
  let openError: ApiError | null = null;
  const open = vi.fn(async (url: string) => {
    if (openError) {
      const err = openError;
      openError = null;
      throw err;
    }
    calls.push({ url });
    const id = /streams\/(\w+)\//.exec(url)![1];
    const session: WhepSession = {
      stream: {} as MediaStream,
      peer: {} as RTCPeerConnection,
      control: async (action, tracks) => {
        const err = refuse.get(action);
        if (err) throw err;
        calls.push({ action: `${id} ${action}`, tracks });
      },
      close: async () => void calls.push({ closed: id }),
    };
    return session;
  });
  return {
    open,
    calls,
    refuse,
    failNextOpen: (e: ApiError) => (openError = e),
  };
}

async function settle() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

test('pre-warms every live stream, then resumes, pauses and closes', async () => {
  const srv = fakeServer();
  const s = new RoomSessions(srv.open);
  s.update([stream('a'), stream('b', 'starting')], { a: 'paused', b: 'paused' });
  await settle();
  expect(srv.calls).toHaveLength(1);
  const url = new URL(srv.calls[0]!.url!, 'http://x');
  expect(url.pathname).toBe('/api/streams/a/whep');
  expect(url.searchParams.get('paused')).toBe('true');
  expect(url.searchParams.get('preview')).toBe('false');
  expect(url.searchParams.get('tab')).toBeTruthy();
  expect(s.getSnapshot().a!).toMatchObject({ status: 'ready', paused: { video: true, audio: true } });

  srv.calls.length = 0;
  s.update([stream('a'), stream('b', 'starting')], { a: 'playing', b: 'paused' });
  await settle();
  expect(srv.calls).toEqual([{ action: 'a resume', tracks: { video: true, audio: true } }]);

  // Hidden tab: audio only pauses video alone.
  srv.calls.length = 0;
  s.update([stream('a')], { a: 'audio' });
  await settle();
  expect(srv.calls).toEqual([{ action: 'a pause', tracks: { video: true, audio: false } }]);
  expect(s.getSnapshot().a!.paused).toEqual({ video: true, audio: false });

  // The same wish again sends nothing.
  srv.calls.length = 0;
  s.update([stream('a')], { a: 'audio' });
  await settle();
  expect(srv.calls).toEqual([]);

  // Reconnecting closes the session; going live again opens a new one.
  s.update([stream('a', 'reconnecting')], { a: 'playing' });
  await settle();
  expect(srv.calls).toEqual([{ closed: 'a' }]);
  srv.calls.length = 0;
  s.update([stream('a')], { a: 'playing' });
  await settle();
  expect(srv.calls).toHaveLength(1);
  expect(new URL(srv.calls[0]!.url!, 'http://x').searchParams.get('paused')).toBeNull(); // opened playing

  // The stream ends: its session closes and its entry goes.
  srv.calls.length = 0;
  s.update([], {});
  await settle();
  expect(srv.calls).toEqual([{ closed: 'a' }]);
  expect(s.getSnapshot()).toEqual({});
});

test('only the latest wish is sent', async () => {
  const srv = fakeServer();
  const s = new RoomSessions(srv.open);
  s.update([stream('a')], { a: 'paused' });
  await settle();
  srv.calls.length = 0;
  const wishes: Want[] = ['playing', 'paused', 'playing', 'audio'];
  for (const w of wishes) s.update([stream('a')], { a: w });
  await settle();
  expect(srv.calls).toEqual([
    { action: 'a resume', tracks: { video: true, audio: true } },
    { action: 'a pause', tracks: { video: true, audio: false } },
  ]);
});

test('a refused resume is not repeated until the wish changes', async () => {
  const srv = fakeServer();
  const s = new RoomSessions(srv.open);
  s.update([stream('a')], { a: 'paused' });
  await settle();
  srv.refuse.set('resume', new ApiError(409, 'you can watch up to 4 streams at once', 'too_many_streams'));
  s.update([stream('a')], { a: 'playing' });
  await settle();
  expect(s.getSnapshot().a!).toMatchObject({ status: 'ready', code: 'too_many_streams' });
  srv.refuse.clear();
  srv.calls.length = 0;
  s.update([stream('a')], { a: 'playing' }); // e.g. a stats update
  await settle();
  expect(srv.calls).toEqual([]);
  s.update([stream('a')], { a: 'paused' });
  s.update([stream('a')], { a: 'playing' });
  await settle();
  expect(srv.calls).toEqual([{ action: 'a resume', tracks: { video: true, audio: true } }]);
});

test('without pausing on the server, paused streams have no session', async () => {
  const srv = fakeServer();
  const s = new RoomSessions(srv.open);
  srv.failNextOpen(new ApiError(501, 'the media server cannot pause sessions', 'pause_unsupported'));
  s.update([stream('a')], { a: 'paused' });
  await settle();
  expect(s.pauseSupported).toBe(false);
  expect(srv.calls).toEqual([]);

  s.update([stream('a')], { a: 'audio' });
  await settle();
  expect(srv.calls).toHaveLength(1); // opened playing; audio only is not possible
  expect(new URL(srv.calls[0]!.url!, 'http://x').searchParams.get('paused')).toBeNull();
  srv.calls.length = 0;
  s.update([stream('a')], { a: 'paused' });
  await settle();
  expect(srv.calls).toEqual([{ closed: 'a' }]);
});

test('a failed open waits for a retry', async () => {
  const srv = fakeServer();
  const s = new RoomSessions(srv.open);
  srv.failNextOpen(new ApiError(403, 'you may not watch this stream', 'not_allowed'));
  s.update([stream('a')], { a: 'playing' });
  await settle();
  expect(s.getSnapshot().a!).toMatchObject({ status: 'error', code: 'not_allowed' });
  s.update([stream('a')], { a: 'playing' });
  await settle();
  expect(srv.open).toHaveBeenCalledTimes(1);
  s.retry('a');
  await settle();
  expect(s.getSnapshot().a!.status).toBe('ready');
});
