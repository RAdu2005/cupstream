import { describe, expect, it, vi } from 'vitest';
import { fakeScreen } from '@/test/fakeMedia';
import {
  captureWarnings,
  defaultSettings,
  describeCapture,
  displayMediaOptions,
  listAudioDevices,
  loadSettings,
  maxBitrateOf,
  pickCodec,
  saveSettings,
  type Captured,
} from './capture';

const screen: Captured = { surface: 'monitor', width: 1920, height: 1080, frameRate: 30, screenAudio: false };

describe('capture settings', () => {
  it('picks a bitrate for the preset unless one is chosen', () => {
    expect(maxBitrateOf(defaultSettings)).toBe(5_000_000);
    expect(maxBitrateOf({ ...defaultSettings, frameRate: 60 })).toBe(8_000_000);
    expect(maxBitrateOf({ ...defaultSettings, maxBitrate: 3_000_000 })).toBe(3_000_000);
  });

  it('asks for the preset as an upper bound', () => {
    const o = displayMediaOptions({ ...defaultSettings, resolution: '720p', frameRate: 60 });
    expect(o.video).toEqual({
      width: { ideal: 1280, max: 1280 },
      height: { ideal: 720, max: 720 },
      frameRate: { ideal: 60, max: 60 },
    });
    expect(o).toMatchObject({ systemAudio: 'include', selfBrowserSurface: 'exclude' });
  });

  it('remembers the settings, and ignores broken ones', () => {
    saveSettings({ ...defaultSettings, codec: 'av1' });
    expect(loadSettings().codec).toBe('av1');
    localStorage.setItem('cupstream.capture', '{"resolution":"8k"}');
    expect(loadSettings()).toEqual(defaultSettings);
    localStorage.setItem('cupstream.capture', 'not json');
    expect(loadSettings()).toEqual(defaultSettings);
    localStorage.clear();
  });

  it('falls back to a codec the browser can send', () => {
    expect(pickCodec('av1', ['h264', 'av1'])).toBe('av1');
    expect(pickCodec('av1', ['h264'])).toBe('h264');
    expect(pickCodec('h264', ['av1'])).toBe('av1');
    expect(pickCodec('h264', [])).toBeNull();
  });
});

describe('captureWarnings', () => {
  const texts = (c: Captured, codec: 'h264' | 'av1' = 'h264', s = defaultSettings) =>
    captureWarnings(c, s, codec).map((w) => `${w.level}: ${w.text}${w.suggestApp ? ' [app]' : ''}`);

  it('warns that system audio includes the Discord call', () => {
    expect(texts({ ...screen, screenAudio: true })).toEqual([
      'warning: Viewers will hear all of your computer’s sound, including your Discord call. [app]',
    ]);
    expect(texts({ ...screen, surface: 'window', screenAudio: true })[0]).toMatch(/^warning: .*Discord call/);
  });

  it('says a tab shares its own sound only', () => {
    expect(texts({ ...screen, surface: 'browser', screenAudio: true })).toEqual(['info: Viewers hear this tab’s sound only.']);
  });

  it('points to the audio devices when the screen brought no sound', () => {
    expect(texts(screen)).toEqual([
      'info: Your browser shares no sound with this screen. Choose an audio device below if viewers should hear something. [app]',
    ]);
  });

  it('reports a smaller capture, a lower frame rate and a codec fallback', () => {
    const got = texts({ ...screen, width: 1280, height: 720, frameRate: 15 }, 'h264', { ...defaultSettings, codec: 'av1' });
    expect(got.slice(1)).toEqual([
      'info: Captured at 1280×720: what you share is smaller than 1080p, so it is not scaled up.',
      'info: Your browser captures 15 frames a second, not 30.',
      'warning: This browser cannot send AV1, so it sends H.264.',
    ]);
    // A wide screen scaled to fit is not "smaller".
    expect(texts({ ...screen, width: 1920, height: 804 })).toHaveLength(1);
  });

  it('describes a captured stream', () => {
    expect(describeCapture(fakeScreen({ audio: true, surface: 'browser' }))).toEqual({
      surface: 'browser',
      width: 1920,
      height: 1080,
      frameRate: 30,
      screenAudio: true,
    });
  });
});

describe('listAudioDevices', () => {
  function stubDevices(labels: string[]) {
    const getUserMedia = vi.fn(async () => {
      labels = labels.map((l, i) => l || `Device ${i}`);
      return { getTracks: () => [{ stop() {} }] };
    });
    vi.stubGlobal('navigator', {
      mediaDevices: {
        enumerateDevices: async () => [
          { kind: 'videoinput', deviceId: 'cam', label: 'Camera' },
          ...labels.map((label, i) => ({ kind: 'audioinput', deviceId: `d${i}`, label })),
        ],
        getUserMedia,
      },
    });
    return getUserMedia;
  }

  it('lists monitors of the computer’s sound first', async () => {
    stubDevices(['Built-in Microphone', 'Monitor of Built-in Audio']);
    expect(await listAudioDevices(false)).toEqual([
      { id: 'd1', label: 'Monitor of Built-in Audio', monitor: true },
      { id: 'd0', label: 'Built-in Microphone', monitor: false },
    ]);
  });

  it('asks for access to learn the names', async () => {
    const ask = stubDevices(['', '']);
    expect((await listAudioDevices(false)).map((d) => d.label)).toEqual(['Audio input 1', 'Audio input 2']);
    expect(ask).not.toHaveBeenCalled();
    expect((await listAudioDevices(true)).map((d) => d.label)).toEqual(['Device 0', 'Device 1']);
    expect(ask).toHaveBeenCalledOnce();
  });
});
