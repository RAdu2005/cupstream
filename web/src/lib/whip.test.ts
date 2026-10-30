import { describe, expect, it } from 'vitest';
import { codecPreferences, videoBitrate, withStereoOpus } from './whip';

const codec = (mimeType: string, sdpFmtpLine?: string) => ({ mimeType, clockRate: 90000, sdpFmtpLine });

const chrome = [
  codec('video/VP8'),
  codec('video/rtx', 'apt=96'),
  codec('video/H264', 'level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f'),
  codec('video/H264', 'level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42e01f'),
  codec('video/H264', 'level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f'),
  codec('video/H264', 'level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=4d001f'),
  codec('video/H264', 'level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=64001f'),
  codec('video/AV1', 'level-idx=5;profile=0;tier=0'),
  codec('video/red'),
];

describe('codecPreferences', () => {
  it('offers only H.264 in packetization mode 1, High first, and RTX', () => {
    expect(codecPreferences(chrome, 'h264').map((c) => `${c.mimeType} ${/profile-level-id=(\w+)/.exec(c.sdpFmtpLine ?? '')?.[1] ?? ''}`)).toEqual([
      'video/H264 64001f',
      'video/H264 4d001f',
      'video/H264 42e01f',
      'video/H264 42001f',
      'video/rtx ',
    ]);
  });

  it('offers only AV1 when chosen', () => {
    expect(codecPreferences(chrome, 'av1').map((c) => c.mimeType)).toEqual(['video/AV1', 'video/rtx']);
  });
});

describe('withStereoOpus', () => {
  const answer = [
    'v=0',
    'm=audio 9 UDP/TLS/RTP/SAVPF 111',
    'a=rtpmap:111 opus/48000/2',
    'a=fmtp:111 minptime=10;useinbandfec=1;stereo=0',
    'm=video 9 UDP/TLS/RTP/SAVPF 96',
    'a=rtpmap:96 H264/90000',
    '',
  ].join('\r\n');

  it('adds stereo and the bitrate to the Opus parameters, keeping the others', () => {
    const out = withStereoOpus(answer);
    expect(out).toContain('a=fmtp:111 minptime=10;useinbandfec=1;stereo=0;sprop-stereo=1;maxaveragebitrate=128000\r\n');
    expect(out.split('\r\n')).toHaveLength(answer.split('\r\n').length);
  });

  it('adds parameters where there were none', () => {
    const out = withStereoOpus('m=audio 9 RTP 111\na=rtpmap:111 opus/48000/2\na=sendonly\n');
    expect(out).toBe('m=audio 9 RTP 111\na=rtpmap:111 opus/48000/2\na=fmtp:111 stereo=1;sprop-stereo=1;maxaveragebitrate=128000\na=sendonly\n');
  });

  it('leaves answers without Opus alone', () => {
    expect(withStereoOpus('m=video 9 RTP 96\r\na=rtpmap:96 AV1/90000\r\n')).toBe('m=video 9 RTP 96\r\na=rtpmap:96 AV1/90000\r\n');
  });
});

it('leaves room for audio within the cap', () => {
  expect(videoBitrate(5_000_000, true)).toBe(4_872_000);
  expect(videoBitrate(5_000_000, false)).toBe(5_000_000);
  expect(videoBitrate(100_000, true)).toBe(300_000);
});
