import { useCallback, useEffect, useRef, useState } from 'react';
import type { StreamReport } from '@/api/types.gen';
import {
  countersOf,
  isBad,
  judge,
  measureDownload,
  refineCapacity,
  remember,
  reportOf,
  STATS_INTERVAL_MS,
  type Counters,
  type Verdict,
} from './bandwidth';
import { realtime } from './realtime';
import type { RoomSessions } from './roomSessions';

/** The download capacity is the tab's, whatever room it shows. */
let capacity: number | null = null;
let test: Promise<number | null> | null = null;

/** Forgets the capacity estimate (tests). */
export function resetCapacity() {
  capacity = null;
  test = null;
}

/**
 * Reads the statistics of the streams that play with video every two
 * seconds, judges whether the viewer's connection keeps up, reports to the
 * server, and estimates the download capacity.
 */
export function useBandwidth(slug: string, sessions: RoomSessions | null, playing: string[]) {
  const [verdict, setVerdict] = useState<Verdict>({ limited: false, struggling: [] });
  const [testing, setTesting] = useState(false);
  const received = useRef(0);
  const key = playing.join(',');

  useEffect(() => {
    if (!sessions || !key) {
      setVerdict({ limited: false, struggling: [] });
      return;
    }
    const ids = key.split(',');
    const prev = new Map<string, Counters>();
    const history = new Map<string, boolean[]>();
    let stopped = false;

    const tick = async () => {
      const peers = sessions.peers();
      const reports: StreamReport[] = [];
      for (const id of ids) {
        const pc = peers.get(id);
        if (!pc) continue;
        let cur: Counters;
        try {
          cur = countersOf((await pc.getStats()).values(), performance.now());
        } catch {
          continue; // closed meanwhile
        }
        const before = prev.get(id);
        prev.set(id, cur);
        // The first interval after a stream starts includes waiting for a key frame.
        if (!before || before.bytes === 0) continue;
        const r = reportOf(id, before, cur);
        reports.push(r);
        remember(history, id, isBad(r));
      }
      if (stopped) return;
      const v = judge(history, ids.filter((id) => history.has(id)));
      received.current = reports.reduce((sum, r) => sum + r.bitrateBps, 0);
      capacity = refineCapacity(capacity, received.current, v.limited);
      setVerdict((old) =>
        old.limited === v.limited && old.struggling.join() === v.struggling.join() ? old : v,
      );
      if (reports.length > 0) {
        realtime.send({
          type: 'viewerReport',
          room: slug,
          report: { limited: v.limited, capacityBps: Math.round(capacity ?? 0), streams: reports },
        });
      }
    };
    const t = setInterval(() => void tick(), STATS_INTERVAL_MS);
    return () => {
      stopped = true;
      clearInterval(t);
    };
  }, [slug, sessions, key]);

  /**
   * The viewer's download capacity in bits per second. The first call runs
   * the download test; what already plays is added, since it shared the
   * connection during the test. Null when the test failed.
   */
  const ensureCapacity = useCallback(async () => {
    if (capacity !== null) return capacity;
    if (!test) {
      const during = received.current;
      setTesting(true);
      test = measureDownload().then((bps) => {
        if (bps !== null) capacity = Math.max(capacity ?? 0, bps + during);
        else test = null; // try again next time
        return capacity;
      });
      void test.finally(() => setTesting(false));
    }
    return test;
  }, []);

  return { verdict, testing, ensureCapacity };
}
