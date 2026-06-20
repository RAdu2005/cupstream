import Hls, { type ErrorData, type HlsConfig } from 'hls.js';

export type HlsHandle = {
  destroy: () => void;
};

export function attachHls(video: HTMLVideoElement, url: string): HlsHandle {
  if (!url) {
    // eslint-disable-next-line no-console
    console.error('[hls] VITE_HLS_URL is not set — check your .env file');
    throw new Error('HLS url missing');
  }
  // Use hls.js everywhere except real Safari (which has the best native HLS).
  // Edge/Chrome have broken or partial native HLS that doesn't work through
  // reverse proxies with certain MediaMTX HLS variants.
  const isSafari = /^((?!chrome|android).)*safari/i.test(navigator.userAgent);
  if (isSafari && video.canPlayType('application/vnd.apple.mpegurl')) {
    video.src = url;
    return {
      destroy() {
        video.removeAttribute('src');
        video.load();
      },
    };
  }

  if (!Hls.isSupported()) {
    throw new Error('HLS not supported in this browser');
  }

  const config: Partial<HlsConfig> = {
    // LL-HLS tuned for ~1-2s glass-to-glass
    enableWorker: true,
    lowLatencyMode: true,
    liveSyncDurationCount: 2,
    liveMaxLatencyDurationCount: 4,
    backBufferLength: 30,
    maxBufferLength: 6,
    maxMaxBufferLength: 10,
    maxLoadingDelay: 4,
    // ensure cookies ride on every segment + playlist request
    xhrSetup: (xhr) => {
      xhr.withCredentials = true;
    },
  };

  const hls = new Hls(config);
  hls.loadSource(url);
  hls.attachMedia(video);

  hls.on(Hls.Events.ERROR, (_event, data: ErrorData) => {
    // surface in console for debugging
    // eslint-disable-next-line no-console
    console.warn('[hls]', data.type, data.details, data.error?.message ?? '');
    if (data.fatal) {
      switch (data.type) {
        case Hls.ErrorTypes.NETWORK_ERROR:
          hls.startLoad();
          break;
        case Hls.ErrorTypes.MEDIA_ERROR:
          hls.recoverMediaError();
          break;
        default:
          hls.destroy();
      }
    }
  });

  return {
    destroy() {
      hls.destroy();
    },
  };
}
