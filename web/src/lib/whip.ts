import { ApiError } from '@/api/client';
import type { ErrorResponse } from '@/api/types.gen';
import { AUDIO_BITRATE, type Codec } from './capture';

const SDP = 'application/sdp';
const TRICKLE = 'application/trickle-ice-sdpfrag';

/** The lowest video bitrate a cap leaves, in bits per second. */
const MIN_VIDEO_BITRATE = 300_000;

export type WhipPublisher = {
  peer: RTCPeerConnection;
  /** Caps the whole stream (video and audio) at bps. */
  setMaxBitrate: (bps: number) => Promise<void>;
  /** Closes the connection; the stream stays, so the sender can reconnect. */
  close: () => void;
};

export type WhipOptions = {
  codec: Codec;
  /** Most the stream may send, video and audio together, in bits per second. */
  maxBitrate: number;
  onState?: (s: RTCPeerConnectionState) => void;
};

/** The video bitrate that leaves room for audio within a cap on the whole stream. */
export function videoBitrate(total: number, withAudio: boolean) {
  return Math.max(MIN_VIDEO_BITRATE, total - (withAudio ? AUDIO_BITRATE : 0));
}

// H.264 profiles by preference: High and Main give better quality per bit
// than Constrained Baseline. Browsers never send B-frames, which viewers'
// decoders could not be relied on for.
const h264Profiles = ['64', '4d', '42e0', '4200'];

function h264Rank(c: RTCRtpCodec) {
  const id = /profile-level-id=([0-9a-f]{6})/i.exec(c.sdpFmtpLine ?? '')?.[1]?.toLowerCase() ?? '';
  const i = h264Profiles.findIndex((p) => id.startsWith(p));
  return i < 0 ? h264Profiles.length : i;
}

/**
 * The codecs to offer, best first: only the chosen one (H.264 in
 * packetization mode 1), and the retransmission codec. Offering nothing
 * else makes sure MediaMTX does not pick something viewers may not decode.
 */
export function codecPreferences(codecs: RTCRtpCodec[], codec: Codec): RTCRtpCodec[] {
  const mime = codec === 'av1' ? 'video/av1' : 'video/h264';
  const chosen = codecs
    .filter((c) => c.mimeType.toLowerCase() === mime)
    .filter((c) => codec !== 'h264' || /packetization-mode=1/.test(c.sdpFmtpLine ?? ''));
  if (codec === 'h264') chosen.sort((a, b) => h264Rank(a) - h264Rank(b));
  return [...chosen, ...codecs.filter((c) => c.mimeType.toLowerCase() === 'video/rtx')];
}

/**
 * Lets the browser send stereo Opus at AUDIO_BITRATE: the answer is what the
 * sender reads, and by default it sends mono speech quality.
 */
export function withStereoOpus(sdp: string) {
  const eol = sdp.includes('\r\n') ? '\r\n' : '\n';
  const pts = [...sdp.matchAll(/^a=rtpmap:(\d+) opus\/48000\/2$/gim)].map((m) => m[1]);
  let lines = sdp.split(eol);
  for (const pt of pts) {
    const extra = ['stereo=1', 'sprop-stereo=1', `maxaveragebitrate=${AUDIO_BITRATE}`];
    const i = lines.findIndex((l) => l.startsWith(`a=fmtp:${pt} `));
    if (i >= 0) {
      const params = lines[i]!.slice(`a=fmtp:${pt} `.length).split(';').filter((p) => p !== '');
      const keys = new Set(params.map((p) => p.split('=')[0]));
      lines[i] = `a=fmtp:${pt} ` + [...params, ...extra.filter((p) => !keys.has(p.split('=')[0]))].join(';');
    } else {
      const at = lines.findIndex((l) => l.toLowerCase() === `a=rtpmap:${pt} opus/48000/2`);
      lines = [...lines.slice(0, at + 1), `a=fmtp:${pt} ${extra.join(';')}`, ...lines.slice(at + 1)];
    }
  }
  return lines.join(eol);
}

async function setEncodingBitrate(sender: RTCRtpSender, bps: number) {
  const p = sender.getParameters();
  if (!p.encodings?.length) p.encodings = [{}];
  if (p.encodings[0]!.maxBitrate === bps) return;
  p.encodings[0]!.maxBitrate = bps;
  await sender.setParameters(p);
}

/**
 * Publishes tracks over WHIP to url with the stream's token. No STUN server
 * is needed: MediaMTX has a public address. Reconnecting is publishing again
 * to the same URL with the same token; the server replaces the old
 * connection.
 */
export async function publishWhip(
  url: string,
  token: string,
  tracks: { video: MediaStreamTrack; audio?: MediaStreamTrack },
  opts: WhipOptions,
): Promise<WhipPublisher> {
  if (typeof RTCPeerConnection === 'undefined') {
    throw new Error('This browser does not support WebRTC.');
  }
  const pc = new RTCPeerConnection({ bundlePolicy: 'max-bundle' });
  const withAudio = !!tracks.audio;
  const video = pc.addTransceiver(tracks.video, {
    direction: 'sendonly',
    sendEncodings: [{ maxBitrate: videoBitrate(opts.maxBitrate, withAudio) }],
  });
  const caps = RTCRtpSender.getCapabilities?.('video');
  if (caps && typeof video.setCodecPreferences === 'function') {
    const prefs = codecPreferences(caps.codecs, opts.codec);
    if (prefs.length > 0) video.setCodecPreferences(prefs);
  }
  const audio = tracks.audio
    ? pc.addTransceiver(tracks.audio, { direction: 'sendonly', sendEncodings: [{ maxBitrate: AUDIO_BITRATE }] })
    : null;
  if (opts.onState) {
    const onState = opts.onState;
    pc.addEventListener('connectionstatechange', () => onState(pc.connectionState));
  }

  let resource: string | null = null;
  let closed = false;
  const pending: string[] = [];
  async function trickle(fragment: string) {
    if (closed || !resource) return;
    try {
      await fetch(resource, { method: 'PATCH', headers: { 'Content-Type': TRICKLE }, body: fragment });
    } catch {
      // A lost candidate is not fatal; others or the host candidates suffice.
    }
  }
  pc.addEventListener('icecandidate', (ev) => {
    const fragment = ev.candidate ? `a=candidate:${ev.candidate.candidate}\r\n` : 'a=end-of-candidates\r\n';
    if (resource) void trickle(fragment);
    else pending.push(fragment);
  });

  try {
    await pc.setLocalDescription(await pc.createOffer());
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': SDP, Authorization: `Bearer ${token}` },
      body: pc.localDescription?.sdp,
    });
    if (!res.ok) {
      let body: ErrorResponse | null = null;
      try {
        body = (await res.json()) as ErrorResponse;
      } catch {
        // not JSON
      }
      throw new ApiError(res.status, body?.error ?? `Publishing failed (${res.status})`, body?.code);
    }
    resource = res.headers.get('Location');
    await pc.setRemoteDescription({ type: 'answer', sdp: withStereoOpus(await res.text()) });
  } catch (err) {
    closed = true;
    pc.close();
    throw err;
  }
  for (const f of pending.splice(0)) void trickle(f);

  return {
    peer: pc,
    async setMaxBitrate(bps: number) {
      if (closed) return;
      await setEncodingBitrate(video.sender, videoBitrate(bps, withAudio));
      if (audio) await setEncodingBitrate(audio.sender, AUDIO_BITRATE);
    },
    close() {
      closed = true;
      pc.close();
    },
  };
}
