/** Media tracks and streams for tests; jsdom has none. */
export class FakeTrack extends EventTarget {
  readyState: 'live' | 'ended' = 'live';
  contentHint = '';
  stopped = false;

  constructor(
    readonly kind: 'video' | 'audio',
    private settings: Record<string, unknown> = {},
  ) {
    super();
  }

  getSettings() {
    return this.settings;
  }

  stop() {
    this.stopped = true;
    this.readyState = 'ended';
  }

  /** The user ended the capture from the browser's own controls. */
  end() {
    this.stop();
    this.dispatchEvent(new Event('ended'));
  }
}

export class FakeStream {
  constructor(readonly tracks: FakeTrack[]) {}
  getTracks() {
    return this.tracks;
  }
  getVideoTracks() {
    return this.tracks.filter((t) => t.kind === 'video');
  }
  getAudioTracks() {
    return this.tracks.filter((t) => t.kind === 'audio');
  }
}

/** A screen capture: 1920×1080 at 30 fps, with screen audio if asked. */
export function fakeScreen(opts: { audio?: boolean; surface?: string; width?: number; height?: number } = {}) {
  const video = new FakeTrack('video', {
    displaySurface: opts.surface ?? 'monitor',
    width: opts.width ?? 1920,
    height: opts.height ?? 1080,
    frameRate: 30,
  });
  const tracks = [video];
  if (opts.audio) tracks.push(new FakeTrack('audio'));
  return new FakeStream(tracks) as unknown as MediaStream & FakeStream;
}
