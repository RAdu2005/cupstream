import { api, ApiError } from '@/api/client';
import type { StartStreamRequest, StartStreamResponse } from '@/api/types.gen';
import { maxBitrateOf, type CaptureSettings, type Codec } from './capture';
import { realtime } from './realtime';
import { publishWhip, type WhipPublisher } from './whip';

/**
 * Streaming from this tab: one live stream at a time, which outlives page
 * changes. It asks the server for a stream, publishes over WHIP, reconnects
 * when the connection drops and warns before the tab is closed.
 */

export type Phase = 'idle' | 'starting' | 'live' | 'reconnecting' | 'ended';

/** A question the user must answer before the stream can start. */
export type Question =
  /** Already live elsewhere: end that stream first? */
  | 'replace'
  /** Discord (auto) found no Discord room: use the link room? */
  | 'linkFallback';

export type SendStats = {
  width: number;
  height: number;
  fps: number;
  /** Sent, video and audio together, in bits per second. */
  bitrate: number;
  codec: string;
  /** Why the browser sends less than asked (Chromium only): "cpu" or "bandwidth". */
  limitation: string;
  /**
   * Connected for a while, but no video leaves the browser: it cannot encode
   * the codec, e.g. Firefox without its OpenH264 plugin, which still lists
   * H.264 among the codecs it can send.
   */
  noVideo: boolean;
};

export type BroadcastState = {
  phase: Phase;
  started?: StartStreamResponse;
  question?: { kind: Question; message: string };
  /** Why the last attempt failed or the stream ended. */
  error?: string;
  viewers: number;
  stats?: SendStats;
};

export type Media = {
  /** The screen capture; its audio track, if any, is the screen's. */
  screen: MediaStream;
  /** The audio to send: the screen's, a device's or none. */
  audio?: MediaStreamTrack;
  settings: CaptureSettings;
  codec: Codec;
  /** A room slug; empty follows the start mode. */
  room?: string;
};

/** How long the server keeps a stream whose sender dropped (gateway reconnectGrace). */
const RECONNECT_WINDOW_MS = 15_000;
/** A connection that stays "disconnected" this long is given up and replaced. */
const DISCONNECTED_GRACE_MS = 3_000;
const STATS_INTERVAL_MS = 2_000;
/** A connection that has sent no video for this long gets a warning. */
const NO_VIDEO_MS = 5_000;
/** The server's state may lag a new connection by this much. */
const SETTLE_MS = 5_000;

export class Broadcast {
  state: BroadcastState = { phase: 'idle', viewers: 0 };

  private listeners = new Set<() => void>();
  private media: Media | null = null;
  private publisher: WhipPublisher | null = null;
  private unsubscribe: (() => void) | null = null;
  private statsTimer: ReturnType<typeof setInterval> | undefined;
  private disconnectTimer: ReturnType<typeof setTimeout> | undefined;
  /** Bumped on every new connection and on stop, so stale callbacks do nothing. */
  private generation = 0;
  private reconnecting = false;
  private connectedAt = 0;
  private lastSent: { at: number; bytes: number } | null = null;

  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => void this.listeners.delete(fn);
  };

  getState = () => this.state;

  /** Whether the tab is streaming or about to. */
  get active() {
    return this.state.phase === 'starting' || this.state.phase === 'live' || this.state.phase === 'reconnecting';
  }

  private set(patch: Partial<BroadcastState>) {
    this.state = { ...this.state, ...patch };
    for (const fn of this.listeners) fn();
  }

  /**
   * Goes live with media, or answers a question the server asked with the
   * same media. The tracks belong to the broadcast from now on: they stop
   * when it does.
   */
  async start(media: Media, answers: { replace?: boolean; linkFallback?: boolean } = {}) {
    if (this.active) return;
    this.media = media;
    const gen = ++this.generation;
    this.set({ phase: 'starting', question: undefined, error: undefined, started: undefined, viewers: 0, stats: undefined });
    this.watchTracks(media);
    addEventListener('beforeunload', this.onBeforeUnload);
    addEventListener('pagehide', this.onPageHide);

    const req: StartStreamRequest = { source: 'web', room: media.room || undefined, ...answers };
    let started: StartStreamResponse;
    try {
      started = await api<StartStreamResponse>('/api/streams', { method: 'POST', json: req });
    } catch (err) {
      if (gen !== this.generation) return;
      const code = err instanceof ApiError ? err.code : undefined;
      const message = err instanceof Error ? err.message : String(err);
      this.detach();
      if (code === 'already_live') {
        this.set({ phase: 'idle', question: { kind: 'replace', message } });
      } else if (code === 'discord_unavailable') {
        this.set({ phase: 'idle', question: { kind: 'linkFallback', message } });
      } else {
        this.set({ phase: 'idle', error: message });
      }
      return; // the tracks stay, for the answer or another try
    }
    if (gen !== this.generation) return;
    this.set({ started });
    this.unsubscribe = realtime.subscribe(started.roomSlug, (msg) => {
      if (msg.type === 'subscribed') {
        const s = msg.streams.find((x) => x.id === started.stream.id);
        if (s) this.set({ viewers: s.viewers });
      } else if (msg.type === 'stream' && msg.stream.id === started.stream.id) {
        this.set({ viewers: msg.stream.viewers });
        // The server lost this sender before the browser noticed, which
        // takes ICE several seconds.
        if (msg.stream.state === 'reconnecting' && this.state.phase === 'live' && Date.now() - this.connectedAt > SETTLE_MS) {
          void this.reconnect();
        }
      } else if (msg.type === 'streamEnded' && msg.streamId === started.stream.id) {
        this.end('Your stream has ended.');
      }
    });
    try {
      await this.connect(gen);
    } catch (err) {
      if (gen !== this.generation) return;
      void api(`/api/streams/${started.stream.id}`, { method: 'DELETE' }).catch(() => undefined);
      this.end(err instanceof Error ? err.message : String(err), false);
    }
  }

  /** Answers the question the server asked: yes retries with the same media, no drops it. */
  answer(yes: boolean) {
    const q = this.state.question;
    const media = this.media;
    if (!q || !media) return;
    if (!yes) {
      this.release();
      this.set({ phase: 'idle', question: undefined });
      return;
    }
    void this.start(media, q.kind === 'replace' ? { replace: true, linkFallback: true } : { linkFallback: true });
  }

  /** Ends the stream and stops capturing. */
  async stop() {
    const started = this.state.started;
    this.end(undefined);
    this.set({ phase: 'idle' });
    if (started) await api(`/api/streams/${started.stream.id}`, { method: 'DELETE' }).catch(() => undefined);
  }

  /** Forgets an ended stream or a refused start, and stops its capture. */
  dismiss() {
    if (this.active) return;
    this.release();
    this.set({ phase: 'idle', error: undefined, question: undefined, started: undefined, stats: undefined });
  }

  /** Caps what the stream sends (the server's bitrate target), within the user's maximum. */
  async setTarget(bps: number) {
    const media = this.media;
    if (!media || !this.publisher) return;
    const max = maxBitrateOf(media.settings);
    await this.publisher.setMaxBitrate(bps > 0 ? Math.min(bps, max) : max);
  }

  /** Publishes again with another codec, keeping the stream. */
  switchCodec(codec: Codec) {
    if (!this.media || !this.active || this.media.codec === codec) return;
    this.media = { ...this.media, codec };
    void this.reconnect();
  }

  /** The codec being sent. */
  get codec(): Codec | null {
    return this.media?.codec ?? null;
  }

  private async connect(gen: number) {
    const media = this.media!;
    const started = this.state.started!;
    const video = media.screen.getVideoTracks()[0];
    if (!video) throw new Error('Nothing is being captured.');
    const pub = await publishWhip(
      started.whipUrl,
      started.token,
      { video, audio: media.audio },
      { codec: media.codec, maxBitrate: maxBitrateOf(media.settings), onState: (s) => this.onPeerState(gen, s) },
    );
    if (gen !== this.generation) {
      pub.close();
      return;
    }
    this.publisher = pub;
    this.lastSent = null;
    clearInterval(this.statsTimer);
    this.statsTimer = setInterval(() => void this.readStats(gen), STATS_INTERVAL_MS);
  }

  private onPeerState(gen: number, s: RTCPeerConnectionState) {
    if (gen !== this.generation) return;
    clearTimeout(this.disconnectTimer);
    if (s === 'connected') {
      this.connectedAt = Date.now();
      this.set({ phase: 'live' });
    } else if (s === 'failed') {
      void this.reconnect();
    } else if (s === 'disconnected') {
      // Often a short blip that ICE recovers from by itself.
      this.set({ phase: 'reconnecting' });
      this.disconnectTimer = setTimeout(() => void this.reconnect(), DISCONNECTED_GRACE_MS);
    }
  }

  /**
   * Publishes again to the same stream until it works or the server has
   * ended the stream. Waits for the network to come back first.
   */
  private async reconnect() {
    if (this.reconnecting || !this.state.started) return;
    this.reconnecting = true;
    const gen = ++this.generation;
    this.publisher?.close();
    this.publisher = null;
    this.set({ phase: 'reconnecting' });
    const deadline = Date.now() + RECONNECT_WINDOW_MS + 5_000;
    try {
      for (let attempt = 0; gen === this.generation; attempt++) {
        if (!navigator.onLine) await waitOnline(deadline - Date.now());
        if (gen !== this.generation) return;
        try {
          await this.connect(gen);
          return; // connected, or failing again, which comes back here
        } catch (err) {
          if (gen !== this.generation) return;
          const status = err instanceof ApiError ? err.status : 0;
          if (status === 401 || status === 403) {
            this.end(err instanceof Error ? err.message : String(err));
            return;
          }
          if (status === 404 || Date.now() > deadline) {
            // The capture stays, so going live again takes one click.
            this.end('The connection was lost for too long, so your stream ended.', false);
            return;
          }
        }
        await sleep(Math.min(4_000, 500 * 2 ** attempt));
      }
    } finally {
      this.reconnecting = false;
    }
  }

  private async readStats(gen: number) {
    const pc = this.publisher?.peer;
    if (!pc || gen !== this.generation) return;
    let report: RTCStatsReport;
    try {
      report = await pc.getStats();
    } catch {
      return;
    }
    if (gen !== this.generation) return;
    let bytes = 0;
    let videoBytes = 0;
    const stats: SendStats = { width: 0, height: 0, fps: 0, bitrate: 0, codec: '', limitation: '', noVideo: false };
    report.forEach((r: RTCStats & Record<string, unknown>) => {
      if (r.type !== 'outbound-rtp') return;
      bytes += Number(r.bytesSent ?? 0);
      if (r.kind !== 'video') return;
      videoBytes += Number(r.bytesSent ?? 0);
      stats.width = Number(r.frameWidth ?? 0);
      stats.height = Number(r.frameHeight ?? 0);
      stats.fps = Math.round(Number(r.framesPerSecond ?? 0));
      stats.limitation = String(r.qualityLimitationReason ?? '');
      const codec = typeof r.codecId === 'string' ? (report.get(r.codecId) as { mimeType?: string } | undefined) : undefined;
      stats.codec = codec?.mimeType?.replace(/^video\//i, '') ?? '';
    });
    const now = performance.now();
    if (this.lastSent && now > this.lastSent.at) {
      stats.bitrate = Math.max(0, ((bytes - this.lastSent.bytes) * 8 * 1000) / (now - this.lastSent.at));
    }
    this.lastSent = { at: now, bytes };
    stats.noVideo = videoBytes === 0 && this.state.phase === 'live' && Date.now() - this.connectedAt > NO_VIDEO_MS;
    this.set({ stats });
  }

  /** Stops the broadcast when the user ends the capture from the browser's own controls. */
  private watchTracks(media: Media) {
    const video = media.screen.getVideoTracks()[0];
    video?.addEventListener('ended', this.onTrackEnded, { once: true });
  }

  private onTrackEnded = () => {
    if (this.active) void this.stop();
    else this.dismiss();
  };

  private onBeforeUnload = (e: BeforeUnloadEvent) => {
    if (!this.active) return;
    e.preventDefault();
    e.returnValue = ''; // older browsers
  };

  /** The tab is going away: end the stream now rather than after the reconnect window. */
  private onPageHide = (e: PageTransitionEvent) => {
    const started = this.state.started;
    if (e.persisted || !started || !this.active) return;
    void fetch(`/api/streams/${started.stream.id}`, { method: 'DELETE', keepalive: true, credentials: 'same-origin' });
  };

  /** Ends the stream locally; with release, the capture stops too. */
  private end(error: string | undefined, release = true) {
    this.generation++;
    this.detach();
    if (release) this.release();
    this.set({ phase: error ? 'ended' : 'idle', error, stats: undefined, question: undefined });
  }

  /** Stops the connection, timers and listeners, keeping the capture. */
  private detach() {
    clearInterval(this.statsTimer);
    clearTimeout(this.disconnectTimer);
    this.publisher?.close();
    this.publisher = null;
    this.unsubscribe?.();
    this.unsubscribe = null;
    removeEventListener('beforeunload', this.onBeforeUnload);
    removeEventListener('pagehide', this.onPageHide);
  }

  /** Stops capturing. */
  private release() {
    const media = this.media;
    this.media = null;
    if (!media) return;
    media.screen.getVideoTracks()[0]?.removeEventListener('ended', this.onTrackEnded);
    for (const t of media.screen.getTracks()) t.stop();
    media.audio?.stop();
  }

  /** The capture of an ended stream, to go live again with it. */
  get leftover(): Media | null {
    return this.active ? null : this.media;
  }
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

function waitOnline(ms: number) {
  return new Promise<void>((resolve) => {
    const done = () => {
      clearTimeout(t);
      removeEventListener('online', done);
      resolve();
    };
    const t = setTimeout(done, Math.max(0, ms));
    addEventListener('online', done);
  });
}

export const broadcast = new Broadcast();
