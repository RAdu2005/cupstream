import { ApiError } from '@/api/client';
import type { Stream } from '@/api/types.gen';
import { realtime } from './realtime';
import { playWhep, type Tracks, type WhepSession } from './whep';

/**
 * What the page wants from a stream's session:
 * - `paused`: open but sending nothing, so that watching it only waits for
 *   a key frame (pre-warmed, or a tile the viewer paused);
 * - `audio`: sound only (the focused stream while the tab is hidden);
 * - `playing`: video and sound.
 */
export type Want = 'paused' | 'audio' | 'playing';

export type SessionStatus = 'connecting' | 'ready' | 'error';

export type SessionView = {
  status: SessionStatus;
  /** The received tracks; null until the session is open. */
  media: MediaStream | null;
  /** The tracks the server holds back. */
  paused: Tracks;
  error?: string;
  /** Machine-readable reason of the error, e.g. "too_many_streams". */
  code?: string;
};

const NONE: Tracks = { video: false, audio: false };
const ALL: Tracks = { video: true, audio: true };

function tracksFor(want: Want): Tracks {
  return want === 'playing' ? NONE : want === 'audio' ? { video: true, audio: false } : ALL;
}

type Entry = {
  streamId: string;
  session: WhepSession | null;
  paused: Tracks;
  view: SessionView;
  busy: boolean;
  dirty: boolean;
  /** Set after a failure; cleared by retry() or when the stream goes live again. */
  failed: boolean;
  /** A wish the server refused, not asked again until the wish changes. */
  refused: Want | null;
};

/**
 * The WHEP sessions of one room page, one per live stream. When the room
 * opens, every live stream gets a session that is paused without a preview
 * frame (pre-warming); watching a stream resumes it. Changes go to the
 * server one at a time per stream, and only the latest wish is applied, so
 * fast clicking does not queue requests.
 *
 * Without pausing on the server (an older media server), paused streams get
 * no session and watching opens one.
 */
export class RoomSessions {
  private entries = new Map<string, Entry>();
  private streams = new Map<string, Stream>();
  private wants = new Map<string, Want>();
  private listeners = new Set<() => void>();
  private snapshot: Record<string, SessionView> = {};
  private closed = false;
  /** False once the server said it cannot pause. */
  pauseSupported = true;

  constructor(private readonly open: typeof playWhep = playWhep) {}

  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => void this.listeners.delete(fn);
  };

  getSnapshot = () => this.snapshot;

  /** The peer connections of open sessions, for statistics. */
  peers(): Map<string, RTCPeerConnection> {
    const out = new Map<string, RTCPeerConnection>();
    for (const [id, e] of this.entries) if (e.session) out.set(id, e.session.peer);
    return out;
  }

  /** Applies the room's streams and what the page wants from each. */
  update(streams: Stream[], wants: Record<string, Want>) {
    if (this.closed) return;
    const prev = this.streams;
    this.streams = new Map(streams.map((s) => [s.id, s]));
    this.wants = new Map(Object.entries(wants));
    for (const s of streams) {
      let e = this.entries.get(s.id);
      if (!e) {
        e = {
          streamId: s.id,
          session: null,
          paused: ALL,
          view: { status: 'connecting', media: null, paused: ALL },
          busy: false,
          dirty: false,
          failed: false,
          refused: null,
        };
        this.entries.set(s.id, e);
      }
      // A stream that comes back after reconnecting gets a fresh try.
      if (s.state === 'live' && prev.get(s.id)?.state !== 'live') e.failed = false;
      this.kick(e);
    }
    for (const e of this.entries.values()) if (!this.streams.has(e.streamId)) this.kick(e);
  }

  /** Tries a failed stream again. */
  retry(streamId: string) {
    const e = this.entries.get(streamId);
    if (!e) return;
    e.failed = false;
    this.setView(e, { status: 'connecting', error: undefined, code: undefined });
    this.kick(e);
  }

  /** Closes every session (leaving the page). */
  close() {
    this.closed = true;
    for (const e of this.entries.values()) void e.session?.close();
    this.entries.clear();
    this.listeners.clear();
  }

  private emit() {
    const next: Record<string, SessionView> = {};
    for (const [id, e] of this.entries) next[id] = e.view;
    this.snapshot = next;
    for (const fn of this.listeners) fn();
  }

  private setView(e: Entry, change: Partial<SessionView>) {
    e.view = { ...e.view, ...change };
    this.emit();
  }

  private kick(e: Entry) {
    e.dirty = true;
    if (e.busy) return;
    e.busy = true;
    void (async () => {
      while (e.dirty && !this.closed) {
        e.dirty = false;
        await this.step(e);
      }
      e.busy = false;
    })();
  }

  private async drop(e: Entry) {
    const s = e.session;
    e.session = null;
    e.paused = ALL;
    await s?.close();
  }

  /** Brings one stream's session a step closer to what the page wants. */
  private async step(e: Entry) {
    const stream = this.streams.get(e.streamId);
    const want = this.wants.get(e.streamId) ?? 'paused';
    if (!stream) {
      await this.drop(e);
      this.entries.delete(e.streamId);
      this.emit();
      return;
    }
    if (stream.state !== 'live' || (want === 'paused' && !this.pauseSupported)) {
      if (e.session) {
        await this.drop(e);
        this.setView(e, { status: 'connecting', media: null, paused: ALL });
      }
      return;
    }
    if (e.failed) return;

    if (!e.session) {
      const startPaused = want !== 'playing' && this.pauseSupported;
      const query = new URLSearchParams({ tab: realtime.tabId });
      if (startPaused) {
        query.set('paused', 'true');
        query.set('preview', 'false');
      }
      this.setView(e, { status: 'connecting', error: undefined, code: undefined });
      try {
        e.session = await this.open(`/api/streams/${e.streamId}/whep?${query}`, (state) => {
          if (state === 'failed' && e.session) {
            void this.drop(e);
            e.failed = true;
            this.setView(e, { status: 'error', media: null, error: 'The connection to the media server failed.' });
          }
        });
      } catch (err) {
        if (err instanceof ApiError && err.code === 'pause_unsupported') {
          this.pauseSupported = false;
          e.dirty = true;
          return;
        }
        e.failed = true;
        this.fail(e, err);
        return;
      }
      if (this.closed || !this.streams.has(e.streamId)) {
        await this.drop(e);
        return;
      }
      e.paused = startPaused ? ALL : NONE;
      this.setView(e, { status: 'ready', media: e.session.stream, paused: e.paused });
      e.dirty = true; // the wish may have changed meanwhile
      return;
    }

    if (want !== e.refused) e.refused = null;
    if (e.refused) return;
    const target = this.pauseSupported ? tracksFor(want) : NONE;
    const toPause = { video: target.video && !e.paused.video, audio: target.audio && !e.paused.audio };
    const toResume = { video: !target.video && e.paused.video, audio: !target.audio && e.paused.audio };
    if (!toPause.video && !toPause.audio && !toResume.video && !toResume.audio) return;
    try {
      if (toPause.video || toPause.audio) {
        await e.session.control('pause', toPause);
        e.paused = { video: e.paused.video || toPause.video, audio: e.paused.audio || toPause.audio };
      }
      if (toResume.video || toResume.audio) {
        await e.session.control('resume', toResume);
        e.paused = target;
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        // The server closed the session (the stream restarted): open another.
        await this.drop(e);
        e.dirty = true;
        return;
      }
      if (err instanceof ApiError && err.code === 'pause_unsupported') {
        this.pauseSupported = false;
        e.dirty = true;
        return;
      }
      // Refused, e.g. too many streams: stay as we are and say why.
      e.refused = want;
      this.setView(e, { paused: e.paused });
      this.fail(e, err);
      return;
    }
    this.setView(e, { status: 'ready', paused: e.paused, error: undefined, code: undefined });
  }

  private fail(e: Entry, err: unknown) {
    const error = err instanceof Error ? err.message : String(err);
    const code = err instanceof ApiError ? err.code : undefined;
    this.setView(e, { status: e.session ? 'ready' : 'error', error, code });
  }
}
