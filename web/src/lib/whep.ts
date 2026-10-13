import { ApiError } from '@/api/client';
import type { ErrorResponse } from '@/api/types.gen';

export type WhepSession = {
  stream: MediaStream;
  peer: RTCPeerConnection;
  close: () => Promise<void>;
};

const SDP = 'application/sdp';
const TRICKLE = 'application/trickle-ice-sdpfrag';

/**
 * Plays a stream over WHEP. No STUN server is needed: MediaMTX has a public
 * address and learns the viewer's address from its connectivity checks.
 */
export async function playWhep(url: string, onState?: (s: RTCPeerConnectionState) => void): Promise<WhepSession> {
  if (typeof RTCPeerConnection === 'undefined') {
    throw new Error('This browser does not support WebRTC.');
  }
  const pc = new RTCPeerConnection();
  pc.addTransceiver('video', { direction: 'recvonly' });
  pc.addTransceiver('audio', { direction: 'recvonly' });

  const stream = new MediaStream();
  pc.addEventListener('track', (ev) => stream.addTrack(ev.track));
  if (onState) pc.addEventListener('connectionstatechange', () => onState(pc.connectionState));

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

  await pc.setLocalDescription(await pc.createOffer());
  const res = await fetch(url, { method: 'POST', headers: { 'Content-Type': SDP }, body: pc.localDescription?.sdp });
  if (!res.ok) {
    pc.close();
    let body: ErrorResponse | null = null;
    try {
      body = (await res.json()) as ErrorResponse;
    } catch {
      // not JSON
    }
    throw new ApiError(res.status, body?.error ?? `Playback failed (${res.status})`, body?.code);
  }
  resource = res.headers.get('Location');
  await pc.setRemoteDescription({ type: 'answer', sdp: await res.text() });
  for (const f of pending.splice(0)) void trickle(f);

  return {
    stream,
    peer: pc,
    async close() {
      if (closed) return;
      closed = true;
      pc.close();
      if (resource) await fetch(resource, { method: 'DELETE' }).catch(() => undefined);
    },
  };
}
