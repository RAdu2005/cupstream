import type { StreamReport } from '@/api/types.gen';

/**
 * Bandwidth warnings (overview, 7.2). Every two seconds the page reads each
 * playing stream's WebRTC statistics. If every stream gets worse together,
 * the viewer's own connection is the bottleneck and a banner suggests
 * pausing streams; if only some do, those streams' senders are, and the
 * streams get a badge instead.
 */

/** How often statistics are read and reported. */
export const STATS_INTERVAL_MS = 2000;
/** A stream is struggling when this many of its last samples were bad. */
const BAD_SAMPLES = 2;
const HISTORY = 3;
/** Warn before opening a stream when the total would pass this share of the capacity. */
export const CAPACITY_SHARE = 0.8;

/** Cumulative counters read from one stream's receivers. */
export type Counters = {
  at: number; // ms
  bytes: number;
  packetsReceived: number;
  packetsLost: number;
  framesDropped: number;
  freezes: number;
  framesPerSecond: number;
  jitter: number; // seconds
};

type Stat = Record<string, unknown> & { type: string; kind?: string };
const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : 0);

/** Sums the inbound RTP statistics of a peer connection's video and audio. */
export function countersOf(stats: Iterable<Stat>, at: number): Counters {
  const c: Counters = { at, bytes: 0, packetsReceived: 0, packetsLost: 0, framesDropped: 0, freezes: 0, framesPerSecond: 0, jitter: 0 };
  for (const s of stats) {
    if (s.type !== 'inbound-rtp') continue;
    c.bytes += num(s.bytesReceived);
    c.packetsReceived += num(s.packetsReceived);
    c.packetsLost += Math.max(0, num(s.packetsLost));
    if (s.kind === 'video') {
      c.framesDropped += num(s.framesDropped);
      c.freezes += num(s.freezeCount); // Chrome only
      c.framesPerSecond = num(s.framesPerSecond);
      c.jitter = num(s.jitter);
    }
  }
  return c;
}

/** What happened between two readings. */
export function reportOf(streamId: string, prev: Counters, cur: Counters): StreamReport {
  const dt = (cur.at - prev.at) / 1000;
  const got = Math.max(0, cur.packetsReceived - prev.packetsReceived);
  const lost = Math.max(0, cur.packetsLost - prev.packetsLost);
  return {
    streamId,
    bitrateBps: dt > 0 ? Math.max(0, Math.round(((cur.bytes - prev.bytes) * 8) / dt)) : 0,
    lossRatio: got + lost > 0 ? lost / (got + lost) : 0,
    freezes: Math.max(0, cur.freezes - prev.freezes),
    framesDropped: Math.max(0, cur.framesDropped - prev.framesDropped),
    framesPerSecond: cur.framesPerSecond,
    jitterMs: cur.jitter * 1000,
  };
}

/** Whether one interval looks bad to a viewer: lost packets, freezes or many dropped frames. */
export function isBad(r: StreamReport) {
  const frames = r.framesPerSecond * (STATS_INTERVAL_MS / 1000);
  return r.lossRatio > 0.03 || r.freezes > 0 || (frames > 0 && r.framesDropped / frames > 0.1);
}

export type Verdict = {
  /** Every playing stream struggles: the viewer's connection cannot keep up. */
  limited: boolean;
  /** Streams that struggle on their own: their sender's connection, most likely. */
  struggling: string[];
};

/**
 * Decides from each playing stream's recent samples (true: bad) whether the
 * viewer's connection is the bottleneck. With a single stream there is
 * nothing to compare, so it only gets a badge.
 */
export function judge(history: Map<string, boolean[]>, playing: string[]): Verdict {
  const bad = playing.filter((id) => (history.get(id) ?? []).filter(Boolean).length >= BAD_SAMPLES);
  if (playing.length >= 2 && bad.length === playing.length) return { limited: true, struggling: [] };
  return { limited: false, struggling: bad };
}

export function remember(history: Map<string, boolean[]>, id: string, bad: boolean) {
  const h = [...(history.get(id) ?? []), bad].slice(-HISTORY);
  history.set(id, h);
}

/**
 * Refines the capacity estimate from what the viewer received: healthy
 * throughput proves at least that much; when the connection is the
 * bottleneck, what got through is about what it can take.
 */
export function refineCapacity(capacity: number | null, receivedBps: number, limited: boolean): number | null {
  if (receivedBps <= 0) return capacity;
  if (limited) return Math.min(capacity ?? Infinity, receivedBps * 1.1);
  return capacity === null ? null : Math.max(capacity, receivedBps);
}

/** Whether opening a stream would pass the share of the capacity worth a warning. */
export function overCapacity(capacity: number | null, playingBps: number, newBps: number) {
  return capacity !== null && playingBps + newBps > CAPACITY_SHARE * capacity;
}

/**
 * Measures the download speed from the server for up to `ms`, timing from
 * the first bytes so the request's round trip does not count. Returns bits
 * per second, or null if the test failed.
 */
export async function measureDownload(
  ms = 1500,
  fetcher: typeof fetch = fetch,
  now: () => number = () => performance.now(),
): Promise<number | null> {
  const abort = new AbortController();
  try {
    const res = await fetcher(`/api/bandwidth-test?bytes=${32 << 20}`, { cache: 'no-store', signal: abort.signal });
    if (!res.ok || !res.body) return null;
    const reader = res.body.getReader();
    let start = 0;
    let bytes = 0;
    for (;;) {
      const { done, value } = await reader.read();
      const t = now();
      if (done) break;
      if (!start) {
        start = t; // the first chunk only starts the clock
        continue;
      }
      bytes += value.byteLength;
      if (t - start >= ms) break;
    }
    const secs = (now() - start) / 1000;
    abort.abort();
    return start && secs > 0 && bytes > 0 ? (bytes * 8) / secs : null;
  } catch {
    return null;
  } finally {
    abort.abort();
  }
}

export function formatMbps(bps: number) {
  return bps >= 10e6 ? `${Math.round(bps / 1e6)} Mbps` : `${(bps / 1e6).toFixed(1)} Mbps`;
}
