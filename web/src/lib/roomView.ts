import type { Want } from './roomSessions';

/** How many streams a viewer may receive at once (the server's limit, 7.1). */
export const MAX_WATCHING = 4;

export type Layout = 'focus' | 'grid';

/** What the viewer chose to watch in a room, and how. */
export type ViewState = {
  /** Streams being watched, in the order they were opened. */
  watched: string[];
  focused: string | null;
  layout: Layout;
  /** Tiles the viewer paused themselves. */
  paused: Record<string, boolean>;
  muted: Record<string, boolean>;
  /** Volume per stream, 0-1. */
  volume: Record<string, number>;
};

export const initialView: ViewState = { watched: [], focused: null, layout: 'focus', paused: {}, muted: {}, volume: {} };

export type ViewAction =
  | { type: 'watch'; id: string }
  | { type: 'focus'; id: string }
  | { type: 'close'; id: string }
  | { type: 'pause'; id: string; paused: boolean }
  | { type: 'mute'; id: string; muted: boolean }
  | { type: 'volume'; id: string; volume: number }
  | { type: 'layout'; layout: Layout }
  /** Drops streams that ended. */
  | { type: 'streams'; ids: string[] };

/** Streams that receive media: watched and not paused by the viewer. */
export function playingCount(v: ViewState) {
  return v.watched.filter((id) => !v.paused[id]).length;
}

/** Whether one more stream may start playing. */
export function canPlayMore(v: ViewState) {
  return playingCount(v) < MAX_WATCHING;
}

/**
 * Focusing a stream unmutes it and mutes the stream that had the focus:
 * only the focused stream is heard unless the viewer unmutes others.
 */
function focus(v: ViewState, id: string): ViewState {
  const muted = { ...v.muted, [id]: false };
  if (v.focused && v.focused !== id) muted[v.focused] = true;
  return { ...v, focused: id, muted, paused: { ...v.paused, [id]: false } };
}

export function reduceView(v: ViewState, a: ViewAction): ViewState {
  switch (a.type) {
    case 'watch':
      if (v.watched.includes(a.id)) return reduceView(v, { type: 'focus', id: a.id });
      if (!canPlayMore(v)) return v;
      return focus({ ...v, watched: [...v.watched, a.id] }, a.id);
    case 'focus':
      if (!v.watched.includes(a.id) || (v.paused[a.id] && !canPlayMore(v))) return v;
      return focus(v, a.id);
    case 'close': {
      const watched = v.watched.filter((id) => id !== a.id);
      const rest = omit(v, a.id);
      if (v.focused !== a.id) return { ...rest, watched };
      const next = watched.find((id) => !v.paused[id]) ?? watched[0] ?? null;
      const out = { ...rest, watched, focused: null };
      return next ? focus(out, next) : out;
    }
    case 'pause':
      if (!a.paused && !v.paused[a.id]) return v;
      if (!a.paused && !canPlayMore(v)) return v;
      return { ...v, paused: { ...v.paused, [a.id]: a.paused } };
    case 'mute':
      return { ...v, muted: { ...v.muted, [a.id]: a.muted } };
    case 'volume':
      return { ...v, volume: { ...v.volume, [a.id]: a.volume }, muted: { ...v.muted, [a.id]: a.volume === 0 } };
    case 'layout':
      return { ...v, layout: a.layout };
    case 'streams': {
      const live = new Set(a.ids);
      let out = v;
      for (const id of v.watched) if (!live.has(id)) out = reduceView(out, { type: 'close', id });
      return out;
    }
  }
}

function omit(v: ViewState, id: string): ViewState {
  const { [id]: _p, ...paused } = v.paused;
  const { [id]: _m, ...muted } = v.muted;
  const { [id]: _v, ...volume } = v.volume;
  return { ...v, paused, muted, volume };
}

export function isMuted(v: ViewState, id: string) {
  return v.muted[id] ?? id !== v.focused;
}

/**
 * What each stream's session should do. Streams that are not watched stay
 * open but paused (pre-warmed). While the tab is hidden, muted streams
 * pause and the ones being heard drop to audio only.
 */
export function wants(v: ViewState, streamIds: string[], hidden: boolean): Record<string, Want> {
  const out: Record<string, Want> = {};
  for (const id of streamIds) {
    if (!v.watched.includes(id) || v.paused[id]) out[id] = 'paused';
    else if (!hidden) out[id] = 'playing';
    else out[id] = isMuted(v, id) ? 'paused' : 'audio';
  }
  return out;
}
