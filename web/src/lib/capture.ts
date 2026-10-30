/**
 * Screen capture for streaming from the browser (overview 6.3): settings and
 * their presets, the getDisplayMedia and audio-device requests, and the
 * warnings, which come from what was actually captured rather than from the
 * browser's name.
 */

export type Resolution = '720p' | '1080p' | '1440p';
export type FrameRate = 30 | 60;
/** What to keep when the encoder must give something up. */
export type Priority = 'detail' | 'motion';
export type Codec = 'h264' | 'av1';

export type CaptureSettings = {
  resolution: Resolution;
  frameRate: FrameRate;
  priority: Priority;
  codec: Codec;
  /** Most the stream may send, video and audio together, in bits per second; 0 picks one for the preset. */
  maxBitrate: number;
};

export const resolutions: Record<Resolution, { width: number; height: number; label: string }> = {
  '720p': { width: 1280, height: 720, label: '720p' },
  '1080p': { width: 1920, height: 1080, label: '1080p' },
  '1440p': { width: 2560, height: 1440, label: '1440p' },
};

export const defaultSettings: CaptureSettings = {
  resolution: '1080p',
  frameRate: 30,
  priority: 'detail',
  codec: 'h264',
  maxBitrate: 0,
};

/** Bitrates for each preset when the user does not choose one. */
const autoBitrate: Record<Resolution, Record<FrameRate, number>> = {
  '720p': { 30: 3_000_000, 60: 4_500_000 },
  '1080p': { 30: 5_000_000, 60: 8_000_000 },
  '1440p': { 30: 9_000_000, 60: 14_000_000 },
};

/** Opus, in bits per second; stereo at this rate carries music well. */
export const AUDIO_BITRATE = 128_000;

export function maxBitrateOf(s: CaptureSettings) {
  return s.maxBitrate > 0 ? s.maxBitrate : autoBitrate[s.resolution][s.frameRate];
}

const SETTINGS_KEY = 'cupstream.capture';

export function loadSettings(): CaptureSettings {
  try {
    const saved = JSON.parse(localStorage.getItem(SETTINGS_KEY) ?? '{}') as Partial<CaptureSettings>;
    const s = { ...defaultSettings, ...saved };
    if (!(s.resolution in resolutions) || ![30, 60].includes(s.frameRate)) return defaultSettings;
    return s;
  } catch {
    return defaultSettings;
  }
}

export function saveSettings(s: CaptureSettings) {
  try {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(s));
  } catch {
    // private mode: the settings last for this page only
  }
}

/** Whether this browser can capture the screen at all (mobile browsers cannot). */
export function canCaptureScreen() {
  return typeof navigator !== 'undefined' && typeof navigator.mediaDevices?.getDisplayMedia === 'function';
}

/**
 * Options for getDisplayMedia. Browsers treat the size and frame rate as an
 * upper bound and scale down; the screen picker decides the rest. Chromium
 * reads the extra options (system audio on Windows, not offering this tab);
 * others ignore them.
 */
export function displayMediaOptions(s: CaptureSettings): DisplayMediaStreamOptions {
  const { width, height } = resolutions[s.resolution];
  return {
    video: {
      width: { ideal: width, max: width },
      height: { ideal: height, max: height },
      frameRate: { ideal: s.frameRate, max: s.frameRate },
    },
    audio: { echoCancellation: false, noiseSuppression: false, autoGainControl: false },
    systemAudio: 'include',
    selfBrowserSurface: 'exclude',
    surfaceSwitching: 'include',
  } as DisplayMediaStreamOptions;
}

/** Asks the browser for a screen, window or tab; must run in a click handler. */
export async function captureScreen(s: CaptureSettings): Promise<MediaStream> {
  const stream = await navigator.mediaDevices.getDisplayMedia(displayMediaOptions(s));
  const video = stream.getVideoTracks()[0];
  if (video) video.contentHint = s.priority === 'motion' ? 'motion' : 'detail';
  return stream;
}

/**
 * Captures an audio input. Processing is off: these are usually "Monitor
 * of …" sources carrying what the computer plays, which echo cancellation
 * and noise suppression would damage.
 */
export async function captureAudioDevice(deviceId: string): Promise<MediaStreamTrack> {
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: {
      deviceId: { exact: deviceId },
      echoCancellation: false,
      noiseSuppression: false,
      autoGainControl: false,
      channelCount: { ideal: 2 },
    },
  });
  const track = stream.getAudioTracks()[0];
  if (!track) throw new Error('The audio device gave no sound.');
  return track;
}

export type AudioDevice = { id: string; label: string; monitor: boolean };

/**
 * Lists audio inputs. Browsers hide their names until the page may use one,
 * so with ask set this first asks for any microphone, then lists.
 */
export async function listAudioDevices(ask: boolean): Promise<AudioDevice[]> {
  const list = async () =>
    (await navigator.mediaDevices.enumerateDevices())
      .filter((d) => d.kind === 'audioinput' && d.deviceId !== '')
      .map((d, i) => ({
        id: d.deviceId,
        label: d.label || `Audio input ${i + 1}`,
        monitor: /^monitor of /i.test(d.label),
      }));
  let devices = await list();
  if (ask && devices.every((d) => !d.label || d.label.startsWith('Audio input '))) {
    const s = await navigator.mediaDevices.getUserMedia({ audio: true });
    for (const t of s.getTracks()) t.stop();
    devices = await list();
  }
  // Monitors first: they carry what the computer plays, which is usually
  // what a streamer wants viewers to hear.
  return devices.sort((a, b) => Number(b.monitor) - Number(a.monitor));
}

/** Codecs this browser can send, among those CupStream accepts. */
export function sendableCodecs(): Codec[] {
  const caps = typeof RTCRtpSender !== 'undefined' ? RTCRtpSender.getCapabilities?.('video') : null;
  if (!caps) return ['h264'];
  const mimes = new Set(caps.codecs.map((c) => c.mimeType.toLowerCase()));
  const out: Codec[] = [];
  if (mimes.has('video/h264')) out.push('h264');
  if (mimes.has('video/av1')) out.push('av1');
  return out;
}

/** The codec to send: the chosen one if possible, otherwise the other. */
export function pickCodec(wanted: Codec, available: Codec[]): Codec | null {
  if (available.includes(wanted)) return wanted;
  return available[0] ?? null;
}

export type Warning = {
  level: 'warning' | 'info';
  text: string;
  /** The desktop app would avoid the problem. */
  suggestApp?: boolean;
};

export type Captured = {
  /** What the user picked: a screen, a window or a browser tab. */
  surface: string | undefined;
  width: number;
  height: number;
  frameRate: number;
  /** Audio came with the screen (system audio, or a tab's). */
  screenAudio: boolean;
};

export function describeCapture(stream: MediaStream): Captured {
  const v = stream.getVideoTracks()[0]?.getSettings() ?? {};
  return {
    surface: (v as MediaTrackSettings & { displaySurface?: string }).displaySurface,
    width: v.width ?? 0,
    height: v.height ?? 0,
    frameRate: Math.round(v.frameRate ?? 0),
    screenAudio: stream.getAudioTracks().length > 0,
  };
}

/** What the user should know about a capture (the table in 6.3). */
export function captureWarnings(c: Captured, s: CaptureSettings, codec: Codec | null): Warning[] {
  const out: Warning[] = [];
  if (c.screenAudio && c.surface !== 'browser') {
    out.push({
      level: 'warning',
      text: 'Viewers will hear all of your computer’s sound, including your Discord call.',
      suggestApp: true,
    });
  } else if (c.screenAudio) {
    out.push({ level: 'info', text: 'Viewers hear this tab’s sound only.' });
  } else {
    out.push({
      level: 'info',
      text: 'Your browser shares no sound with this screen. Choose an audio device below if viewers should hear something.',
      suggestApp: true,
    });
  }

  const want = resolutions[s.resolution];
  if (c.width > 0 && c.width < want.width * 0.9 && c.height < want.height * 0.9) {
    out.push({
      level: 'info',
      text: `Captured at ${c.width}×${c.height}: what you share is smaller than ${want.label}, so it is not scaled up.`,
    });
  }
  if (c.frameRate > 0 && c.frameRate < s.frameRate * 0.8) {
    out.push({ level: 'info', text: `Your browser captures ${c.frameRate} frames a second, not ${s.frameRate}.` });
  }
  if (codec && codec !== s.codec) {
    out.push({
      level: 'warning',
      text: `This browser cannot send ${codecLabel[s.codec]}, so it sends ${codecLabel[codec]}.`,
    });
  }
  return out;
}

export const codecLabel: Record<Codec, string> = { h264: 'H.264', av1: 'AV1' };

/** Whether the label names a device that carries the computer's own sound. */
export function hasMonitorDevices(devices: AudioDevice[]) {
  return devices.some((d) => d.monitor);
}
