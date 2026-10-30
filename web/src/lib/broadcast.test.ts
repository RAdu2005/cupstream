import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@/api/client';
import type { StartStreamResponse, Stream } from '@/api/types.gen';
import { fakeScreen } from '@/test/fakeMedia';
import { FakeWebSocket } from '@/test/fakeWebSocket';
import { Broadcast, type Media } from './broadcast';
import { defaultSettings } from './capture';
import { startThumbnails, uploadThumbnail } from './thumbnails';
import { publishWhip, type WhipOptions } from './whip';

vi.mock('./whip', () => ({ publishWhip: vi.fn() }));
vi.mock('./thumbnails', () => ({ startThumbnails: vi.fn(() => stopThumbnails), uploadThumbnail: vi.fn(async () => true) }));
const stopThumbnails = vi.fn();

const stream: Stream = {
  id: 'abcdefghijklmnop',
  user: { id: 'u1', handle: 'alice', displayName: 'Alice' },
  source: 'web',
  state: 'starting',
  startedAt: 1,
  viewers: 0,
  bitrate: 0,
  lossRatio: 0,
};
const started: StartStreamResponse = {
  stream,
  roomSlug: 'alice-room',
  whipUrl: 'http://localhost/api/streams/abcdefghijklmnop/whip',
  token: 'tok',
};

type Call = { method: string; path: string; body?: unknown };

/** Answers API calls in order; each reply is a status and a body. */
function stubApi(...replies: [number, unknown][]) {
  const calls: Call[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init: RequestInit = {}) => {
      calls.push({ method: init.method ?? 'GET', path, body: init.body ? JSON.parse(init.body as string) : undefined });
      const [status, body] = replies.shift() ?? [204, undefined];
      return new Response(status === 204 ? null : JSON.stringify(body), { status });
    }),
  );
  return calls;
}

/** A publisher whose connection state the test drives. */
function stubPublish() {
  const peers: { onState: (s: RTCPeerConnectionState) => void; closed: boolean; maxBitrate: number[] }[] = [];
  vi.mocked(publishWhip).mockImplementation(async (_url, _token, _tracks, opts: WhipOptions) => {
    const p = { onState: opts.onState!, closed: false, maxBitrate: [] as number[] };
    peers.push(p);
    return {
      peer: { getStats: async () => new Map() } as unknown as RTCPeerConnection,
      setMaxBitrate: async (bps: number) => void p.maxBitrate.push(bps),
      close: () => void (p.closed = true),
    };
  });
  return peers;
}

function media(audio = false): Media {
  return { screen: fakeScreen({ audio }), settings: defaultSettings, codec: 'h264' };
}

const flush = () => new Promise((r) => setTimeout(r, 0));

let b: Broadcast;
beforeEach(() => {
  vi.stubGlobal('WebSocket', FakeWebSocket);
  b = new Broadcast();
});
afterEach(async () => {
  await b.stop();
  vi.mocked(publishWhip).mockReset();
});

describe('Broadcast', () => {
  it('goes live: asks for a stream, publishes to it and follows the connection', async () => {
    const calls = stubApi([201, started]);
    const peers = stubPublish();
    const m = media(true);
    await b.start(m);

    expect(calls[0]).toEqual({ method: 'POST', path: '/api/streams', body: { source: 'web' } });
    expect(publishWhip).toHaveBeenCalledWith(
      started.whipUrl,
      'tok',
      { video: m.screen.getVideoTracks()[0], audio: undefined },
      expect.objectContaining({ codec: 'h264', maxBitrate: 5_000_000 }),
    );
    expect(b.state.phase).toBe('starting');
    peers[0]!.onState('connected');
    expect(b.state.phase).toBe('live');
    expect(b.active).toBe(true);

    // It follows its room, for the viewer count and the end of the stream.
    FakeWebSocket.last.open();
    expect(FakeWebSocket.last.sent).toContainEqual({ type: 'subscribe', room: 'alice-room' });
    FakeWebSocket.last.receive({ type: 'stream', room: 'alice-room', stream: { ...stream, state: 'live', viewers: 3 } });
    expect(b.state.viewers).toBe(3);
  });

  it('asks before replacing a live stream, then retries with the same capture', async () => {
    const calls = stubApi(
      [409, { error: 'you are already live (from OBS); end that stream first', code: 'already_live' }],
      [201, started],
    );
    stubPublish();
    const m = media();
    await b.start(m);
    expect(b.state).toMatchObject({ phase: 'idle', question: { kind: 'replace' } });
    expect(b.active).toBe(false);
    expect(m.screen.getVideoTracks()[0]!.readyState).toBe('live');

    b.answer(true);
    await flush();
    expect(calls[1]?.body).toEqual({ source: 'web', replace: true, linkFallback: true });
    expect(b.state.phase).toBe('starting');
  });

  it('offers the link room when Discord (auto) finds no Discord room', async () => {
    const calls = stubApi([409, { error: 'you are not in a Discord voice channel', code: 'discord_unavailable' }], [201, started]);
    stubPublish();
    await b.start({ ...media(), room: '' });
    expect(b.state.question?.kind).toBe('linkFallback');
    b.answer(true);
    await flush();
    expect(calls[1]?.body).toEqual({ source: 'web', linkFallback: true });
  });

  it('stops the capture when the user says no', async () => {
    stubApi([409, { error: 'already live', code: 'already_live' }]);
    const m = media();
    await b.start(m);
    b.answer(false);
    expect(b.state).toMatchObject({ phase: 'idle', question: undefined });
    expect(m.screen.getVideoTracks()[0]!.readyState).toBe('ended');
  });

  it('reports other refusals and keeps the capture for another try', async () => {
    stubApi([403, { error: 'you may not stream in this room', code: 'not_allowed' }]);
    const m = media();
    await b.start({ ...m, room: 'bobs-room' });
    expect(b.state).toMatchObject({ phase: 'idle', error: 'you may not stream in this room' });
    expect(b.leftover?.screen).toBe(m.screen);
  });

  it('reconnects to the same stream when the connection fails', async () => {
    stubApi([201, started]);
    const peers = stubPublish();
    await b.start(media());
    peers[0]!.onState('connected');
    peers[0]!.onState('failed');
    expect(b.state.phase).toBe('reconnecting');
    await flush();
    expect(peers[0]!.closed).toBe(true);
    expect(publishWhip).toHaveBeenCalledTimes(2);
    expect(vi.mocked(publishWhip).mock.calls[1]?.slice(0, 2)).toEqual([started.whipUrl, 'tok']);
    // A callback of the old connection changes nothing.
    peers[0]!.onState('connected');
    expect(b.state.phase).toBe('reconnecting');
    peers[1]!.onState('connected');
    expect(b.state.phase).toBe('live');
  });

  it('waits a moment before replacing a disconnected connection', async () => {
    vi.useFakeTimers();
    stubApi([201, started]);
    const peers = stubPublish();
    await b.start(media());
    peers[0]!.onState('connected');
    peers[0]!.onState('disconnected');
    expect(b.state.phase).toBe('reconnecting');
    peers[0]!.onState('connected'); // ICE recovered by itself
    await vi.advanceTimersByTimeAsync(5_000);
    expect(publishWhip).toHaveBeenCalledTimes(1);
    expect(b.state.phase).toBe('live');

    peers[0]!.onState('disconnected');
    await vi.advanceTimersByTimeAsync(3_000);
    expect(publishWhip).toHaveBeenCalledTimes(2);
  });

  it('reconnects at once when the server lost the sender', async () => {
    vi.useFakeTimers();
    stubApi([201, started]);
    const peers = stubPublish();
    await b.start(media());
    FakeWebSocket.last.open();
    peers[0]!.onState('connected');
    // Just after connecting, the server's state may be behind.
    FakeWebSocket.last.receive({ type: 'stream', room: 'alice-room', stream: { ...stream, state: 'reconnecting' } });
    expect(publishWhip).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(6_000);
    FakeWebSocket.last.receive({ type: 'stream', room: 'alice-room', stream: { ...stream, state: 'reconnecting' } });
    await vi.advanceTimersByTimeAsync(0);
    expect(publishWhip).toHaveBeenCalledTimes(2);
  });

  it('ends when the server has ended the stream, keeping the capture to go live again', async () => {
    vi.useFakeTimers();
    stubApi([201, started]);
    const peers = stubPublish();
    const m = media();
    await b.start(m);
    peers[0]!.onState('connected');
    vi.mocked(publishWhip).mockRejectedValueOnce(new ApiError(503, 'unavailable')).mockRejectedValueOnce(new ApiError(404, 'this stream has ended'));
    peers[0]!.onState('failed');
    await vi.advanceTimersByTimeAsync(2_000);
    expect(publishWhip).toHaveBeenCalledTimes(3);
    expect(b.state).toMatchObject({ phase: 'ended', error: 'The connection was lost for too long, so your stream ended.' });
    expect(b.active).toBe(false);
    expect(b.leftover?.screen).toBe(m.screen);
  });

  it('ends and stops capturing when the stream was ended elsewhere', async () => {
    stubApi([201, started]);
    const peers = stubPublish();
    const m = media();
    await b.start(m);
    FakeWebSocket.last.open();
    peers[0]!.onState('connected');
    FakeWebSocket.last.receive({ type: 'streamEnded', room: 'alice-room', streamId: stream.id });
    expect(b.state).toMatchObject({ phase: 'ended', error: 'Your stream has ended.' });
    expect(m.screen.getVideoTracks()[0]!.readyState).toBe('ended');
    expect(FakeWebSocket.last.sent).toContainEqual({ type: 'unsubscribe', room: 'alice-room' });
  });

  it('stops: ends the stream on the server and the capture', async () => {
    const calls = stubApi([201, started]);
    const peers = stubPublish();
    const m = media(true);
    await b.start(m);
    peers[0]!.onState('connected');
    await b.stop();
    expect(calls.at(-1)).toMatchObject({ method: 'DELETE', path: `/api/streams/${stream.id}` });
    expect(peers[0]!.closed).toBe(true);
    expect(m.screen.getTracks().every((t) => t.readyState === 'ended')).toBe(true);
    expect(b.state.phase).toBe('idle');
  });

  it('stops when the user ends the capture in the browser', async () => {
    const calls = stubApi([201, started]);
    stubPublish();
    const m = media();
    await b.start(m);
    (m.screen.getVideoTracks()[0] as unknown as { end(): void }).end();
    await flush();
    expect(b.state.phase).toBe('idle');
    expect(calls.at(-1)?.method).toBe('DELETE');
  });

  it('warns before the tab closes while streaming, and ends the stream when it does', async () => {
    const calls = stubApi([201, started]);
    stubPublish();
    await b.start(media());
    const before = new Event('beforeunload', { cancelable: true });
    dispatchEvent(before);
    expect(before.defaultPrevented).toBe(true);

    dispatchEvent(Object.assign(new Event('pagehide'), { persisted: false }));
    expect(calls.at(-1)).toMatchObject({ method: 'DELETE', path: `/api/streams/${stream.id}` });

    await b.stop();
    const after = new Event('beforeunload', { cancelable: true });
    dispatchEvent(after);
    expect(after.defaultPrevented).toBe(false);
  });

  it('applies a bitrate target within the user’s maximum', async () => {
    stubApi([201, started]);
    const peers = stubPublish();
    await b.start({ ...media(), settings: { ...defaultSettings, maxBitrate: 4_000_000 } });
    await b.setTarget(2_500_000);
    await b.setTarget(9_000_000);
    await b.setTarget(0);
    expect(peers[0]!.maxBitrate).toEqual([2_500_000, 4_000_000, 4_000_000]);
  });

  it('switches codec on the same stream', async () => {
    stubApi([201, started]);
    const peers = stubPublish();
    await b.start(media());
    peers[0]!.onState('connected');
    b.switchCodec('av1');
    await flush();
    expect(vi.mocked(publishWhip).mock.calls[1]?.[3]).toMatchObject({ codec: 'av1' });
    expect(b.codec).toBe('av1');
  });

  it('uploads thumbnails of the capture while streaming', async () => {
    stubApi([201, started]);
    stubPublish();
    const m = media();
    await b.start(m);
    expect(startThumbnails).toHaveBeenCalledWith(m.screen.getVideoTracks()[0], expect.any(Function));
    const upload = vi.mocked(startThumbnails).mock.calls.at(-1)![1];
    const jpeg = new Blob(['x'], { type: 'image/jpeg' });
    await upload(jpeg);
    expect(uploadThumbnail).toHaveBeenCalledWith(stream.id, 'tok', jpeg);
    await b.stop();
    expect(stopThumbnails).toHaveBeenCalled();
  });

  it('applies the server’s bitrate target within the user’s maximum, and again after reconnecting', async () => {
    stubApi([201, started]);
    const peers = stubPublish();
    await b.start(media()); // automatic maximum: 5 Mbps
    FakeWebSocket.last.open();
    peers[0]!.onState('connected');
    const target = (maxBitrate: number, congested = 1, streamId = stream.id) =>
      FakeWebSocket.last.receive({ type: 'bitrateTarget', room: 'alice-room', streamId, maxBitrate, viewers: 3, congested });

    target(3_000_000);
    await flush();
    expect(peers[0]!.maxBitrate).toEqual([3_000_000]);
    expect(b.state.target).toEqual({ maxBitrate: 3_000_000, viewers: 3, congested: 1 });
    target(3_000_000); // repeated every two seconds
    target(2_000_000, 1, 'another-stream');
    await flush();
    expect(peers[0]!.maxBitrate).toEqual([3_000_000]);

    peers[0]!.onState('failed');
    await flush();
    expect(peers[1]!.maxBitrate).toEqual([3_000_000]);

    target(9_000_000, 0); // above the user's maximum
    await flush();
    expect(peers[1]!.maxBitrate).toEqual([3_000_000, 5_000_000]);
    expect(b.state.target).toBeUndefined();
    target(0, 0); // lifted
    await flush();
    expect(peers[1]!.maxBitrate.at(-1)).toBe(5_000_000);
  });
});
