import { vi } from 'vitest';
import { ApiError } from '@/api/client';
import type { Stream } from '@/api/types.gen';
import { RoomSessions, type Want } from './roomSessions';
import type { Tracks, WhepSession } from './whep';

const alice = { id: 'u1', handle: 'alice', displayName: 'Alice' };
const stream = (id: string, state: Stream['state'] = 'live', lowQuality: Stream['lowQuality'] = 'none'): Stream => ({
  id,
  user: alice,
  source: 'obs',
  state,
  startedAt: 1,
  viewers: 0,
  bitrate: 0,
  lossRatio: 0,
  lowQuality,
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
    const id = /streams\/(\w+)\//.exec(url)![1] + (url.includes('quality=low') ? ' low' : '');
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

const path = (c: Call) => {
  const u = new URL(c.url!, 'http://x');
  return `${u.pathname}?quality=${u.searchParams.get('quality') ?? ''}&paused=${u.searchParams.get('paused') ?? ''}`;
};

test('switches to the low-quality version and back, keeping the source pre-warmed', async () => {
  const srv = fakeServer();
  const s = new RoomSessions(srv.open);
  const a = stream('a', 'live', 'sent');
  s.update([a], { a: 'playing' });
  await settle();
  srv.calls.length = 0;

  s.update([a], { a: 'playing' }, { a: 'low' });
  await settle();
  // The low version opens playing; the source is paused, not closed.
  expect(path(srv.calls[0]!)).toBe('/api/streams/a/whep?quality=low&paused=');
  expect(srv.calls.slice(1)).toEqual([{ action: 'a pause', tracks: { video: true, audio: true } }]);
  expect(s.getSnapshot().a!).toMatchObject({ status: 'ready', quality: 'low', paused: { video: false, audio: false } });

  // Pausing the tile pauses the low version.
  srv.calls.length = 0;
  s.update([a], { a: 'paused' }, { a: 'low' });
  await settle();
  expect(srv.calls).toEqual([{ action: 'a low pause', tracks: { video: true, audio: true } }]);

  // Back to the source: its session resumes, the low one closes.
  srv.calls.length = 0;
  s.update([a], { a: 'playing' }, { a: 'source' });
  await settle();
  expect(srv.calls).toEqual([{ action: 'a resume', tracks: { video: true, audio: true } }, { closed: 'a low' }]);
  expect(srv.open).toHaveBeenCalledTimes(2);
  expect(s.getSnapshot().a!).toMatchObject({ quality: 'source', paused: { video: false, audio: false } });
});

test('plays the source when the low-quality version is missing or refused', async () => {
  const srv = fakeServer();
  const s = new RoomSessions(srv.open);
  // No low version: the wish is ignored.
  s.update([stream('a')], { a: 'playing' }, { a: 'low' });
  await settle();
  expect(srv.calls.map(path)).toEqual(['/api/streams/a/whep?quality=&paused=']);

  // Refused when it is opened: the source plays instead, without asking again.
  const b = stream('b', 'live', 'sent');
  srv.calls.length = 0;
  srv.failNextOpen(new ApiError(409, 'no low-quality version', 'low_quality_unavailable'));
  s.update([stream('a'), b], { a: 'playing', b: 'playing' }, { a: 'low', b: 'low' });
  await settle();
  expect(srv.calls.map(path)).toEqual(['/api/streams/b/whep?quality=&paused=']);
  expect(s.getSnapshot().b!).toMatchObject({ status: 'ready', quality: 'source' });
  srv.calls.length = 0;
  s.update([stream('a'), b], { a: 'playing', b: 'playing' }, { a: 'low', b: 'low' });
  await settle();
  expect(srv.calls).toEqual([]);
});

test('goes back to the source when the low-quality version ends', async () => {
  const srv = fakeServer();
  const s = new RoomSessions(srv.open);
  s.update([stream('a', 'live', 'sent')], { a: 'playing' }, { a: 'low' });
  await settle();
  expect(s.getSnapshot().a!.quality).toBe('low');
  srv.calls.length = 0;
  s.update([stream('a', 'live', 'none')], { a: 'playing' }, { a: 'low' });
  await settle();
  expect(srv.calls.map((c) => (c.url ? path(c) : c))).toEqual([
    '/api/streams/a/whep?quality=&paused=',
    { closed: 'a low' },
  ]);
  expect(s.getSnapshot().a!.quality).toBe('source');
});

test('a paused stream does not keep a transcode running', async () => {
  const srv = fakeServer();
  const s = new RoomSessions(srv.open);
  const a = stream('a', 'live', 'transcoded');
  s.update([a], { a: 'playing' });
  await settle();
  s.update([a], { a: 'playing' }, { a: 'low' });
  await settle();
  srv.calls.length = 0;
  s.update([a], { a: 'paused' }, { a: 'low' });
  await settle();
  // The pre-warmed source takes over, still paused; the transcode's session closes.
  expect(srv.calls).toEqual([{ closed: 'a low' }]);
  expect(s.getSnapshot().a!).toMatchObject({ quality: 'source', paused: { video: true, audio: true } });
});
