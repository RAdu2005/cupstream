import type { Stream } from '@/api/types.gen';
import { SENDER_LOSS, type Verdict } from './bandwidth';
import type { Quality } from './roomSessions';

/**
 * The viewer's quality choice for a stream (overview, 7.3). Auto plays the
 * source and switches to the low-quality version, when the stream has one,
 * while the viewer's own connection cannot keep up.
 */
export type QualityChoice = 'auto' | 'source' | 'low';

const STORAGE_KEY = 'cupstream.quality';

/** The browser's remembered choice, which streams start with. */
export function loadChoice(): QualityChoice {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    if (v === 'source' || v === 'low') return v;
  } catch {
    // storage unavailable
  }
  return 'auto';
}

export function saveChoice(c: QualityChoice) {
  try {
    localStorage.setItem(STORAGE_KEY, c);
  } catch {
    // storage unavailable
  }
}

/** Whether a stream has a low-quality version a viewer can pick. */
export function hasLow(s: Stream) {
  return s.lowQuality !== 'none';
}

/** The version a stream plays for a choice and Auto's decision. */
export function qualityOf(s: Stream, choice: QualityChoice, autoLow: boolean): Quality {
  if (!hasLow(s)) return 'source';
  return choice === 'low' || (choice === 'auto' && autoLow) ? 'low' : 'source';
}

/** The bitrate a viewer receives of a stream's version, when known. */
export function bitrateOf(s: Stream, q: Quality) {
  return q === 'low' ? (s.lowBitrate ?? 0) : s.bitrate;
}

/**
 * The playing streams whose viewer's connection is the bottleneck: all of
 * them when they get worse together, or a single playing stream that
 * struggles while the server receives it from its sender without losses.
 */
export function viewerBottleneck(verdict: Verdict, playing: Stream[]): string[] {
  if (verdict.limited) return playing.map((s) => s.id);
  if (playing.length === 1 && verdict.struggling.includes(playing[0]!.id) && playing[0]!.lossRatio < SENDER_LOSS) {
    return [playing[0]!.id];
  }
  return [];
}

/** Auto tries the source again this long after switching to low, then twice as long each time. */
export const AUTO_RETRY_MS = 60_000;
const AUTO_RETRY_MAX_MS = 15 * 60_000;

/** Auto's decision for one stream. */
export type AutoEntry = { low: boolean; since: number; retryMs: number };
export type AutoState = Record<string, AutoEntry>;

/**
 * Moves Auto's decisions after a statistics reading: streams on Auto whose
 * viewer is the bottleneck switch to their low version, and try the source
 * again after a while, waiting longer each time it did not work.
 */
export function stepAuto(
  state: AutoState,
  playing: Stream[],
  choices: Record<string, QualityChoice>,
  bottleneck: string[],
  now: number,
): AutoState {
  let out = state;
  const set = (id: string, e: AutoEntry) => {
    if (out === state) out = { ...state };
    out[id] = e;
  };
  for (const s of playing) {
    if (choices[s.id] !== 'auto' || !hasLow(s)) continue;
    const cur = state[s.id];
    if (!cur?.low && bottleneck.includes(s.id)) {
      set(s.id, { low: true, since: now, retryMs: cur?.retryMs ?? AUTO_RETRY_MS });
    } else if (cur?.low && now - cur.since >= cur.retryMs) {
      set(s.id, { low: false, since: now, retryMs: Math.min(cur.retryMs * 2, AUTO_RETRY_MAX_MS) });
    } else if (cur && !cur.low && cur.retryMs > AUTO_RETRY_MS && now - cur.since >= AUTO_RETRY_MAX_MS) {
      // The source has been fine for a long time: start over.
      set(s.id, { ...cur, retryMs: AUTO_RETRY_MS });
    }
  }
  return out;
}
