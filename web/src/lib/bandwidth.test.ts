import type { StreamReport } from '@/api/types.gen';
import {
  countersOf,
  isBad,
  judge,
  measureDownload,
  overCapacity,
  refineCapacity,
  remember,
  reportOf,
} from './bandwidth';


test('reads video and audio receivers and the change between two readings', () => {
  const at = (t: number, v: Record<string, number>, a: Record<string, number>) =>
    countersOf(
      [
        { type: 'inbound-rtp', kind: 'video', ...v },
        { type: 'inbound-rtp', kind: 'audio', ...a },
        { type: 'candidate-pair', bytesReceived: 1e9 },
      ],
      t,
    );
  const prev = at(0, { bytesReceived: 1000, packetsReceived: 100, packetsLost: 0, framesDropped: 1, freezeCount: 0 }, { bytesReceived: 0, packetsReceived: 50, packetsLost: 0 });
  const cur = at(
    2000,
    { bytesReceived: 501000, packetsReceived: 190, packetsLost: 5, framesDropped: 3, freezeCount: 1, framesPerSecond: 30, jitter: 0.012 },
    { bytesReceived: 16000, packetsReceived: 150, packetsLost: 5 },
  );
  const r = reportOf('s1', prev, cur);
  expect(r).toEqual({
    streamId: 's1',
    bitrateBps: 2_064_000,
    lossRatio: 10 / 200,
    freezes: 1,
    framesDropped: 2,
    framesPerSecond: 30,
    jitterMs: 12,
  });
  expect(isBad(r)).toBe(true);
});

const report = (r: Partial<StreamReport>): StreamReport => ({
  streamId: 's',
  bitrateBps: 1e6,
  lossRatio: 0,
  freezes: 0,
  framesDropped: 0,
  framesPerSecond: 30,
  jitterMs: 1,
  ...r,
});

test('what counts as a bad interval', () => {
  expect(isBad(report({}))).toBe(false);
  expect(isBad(report({ lossRatio: 0.02 }))).toBe(false);
  expect(isBad(report({ lossRatio: 0.05 }))).toBe(true);
  expect(isBad(report({ freezes: 1 }))).toBe(true);
  expect(isBad(report({ framesDropped: 10 }))).toBe(true); // of 60 frames
});

test('all streams worse together: the viewer; some: their senders', () => {
  const h = new Map<string, boolean[]>();
  for (const bad of [true, false, true]) remember(h, 'a', bad);
  for (const bad of [true, true]) remember(h, 'b', bad);
  for (const bad of [false, true, false]) remember(h, 'c', bad);
  expect(judge(h, ['a', 'b'])).toEqual({ limited: true, struggling: [] });
  expect(judge(h, ['a', 'b', 'c'])).toEqual({ limited: false, struggling: ['a', 'b'] });
  // One stream alone cannot tell: a badge.
  expect(judge(h, ['a'])).toEqual({ limited: false, struggling: ['a'] });
  // Only the last three samples count.
  for (const bad of [false, false]) remember(h, 'a', bad);
  expect(judge(h, ['a', 'b'])).toEqual({ limited: false, struggling: ['b'] });
});

test('capacity estimate and the warning threshold', () => {
  expect(refineCapacity(null, 5e6, false)).toBeNull(); // only a test starts an estimate
  expect(refineCapacity(10e6, 12e6, false)).toBe(12e6);
  expect(refineCapacity(10e6, 5e6, false)).toBe(10e6);
  expect(refineCapacity(10e6, 5e6, true)).toBeCloseTo(5.5e6);
  expect(overCapacity(10e6, 4e6, 3e6)).toBe(false);
  expect(overCapacity(10e6, 4e6, 5e6)).toBe(true);
  expect(overCapacity(null, 4e6, 50e6)).toBe(false);
});

test('the download test times from the first chunk', async () => {
  let clock = 0;
  const chunks = [100, 1000, 1000, 1000];
  const fetcher = async () =>
    new Response(
      new ReadableStream({
        pull(c) {
          const n = chunks.shift();
          if (n === undefined) return c.close();
          clock += 100; // each chunk arrives 100 ms later
          c.enqueue(new Uint8Array(n));
        },
      }, { highWaterMark: 0 }),
    );
  // 3000 bytes in 300 ms after the first chunk: 80 kbps.
  expect(await measureDownload(5000, fetcher as typeof fetch, () => clock)).toBe(80_000);
  expect(await measureDownload(5000, (async () => new Response('', { status: 503 })) as typeof fetch)).toBeNull();
});
