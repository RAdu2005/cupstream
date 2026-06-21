export type WhepOptions = {
  url: string;
  iceServers?: RTCIceServer[];
  video?: boolean;
  audio?: boolean;
  onStateChange?: (state: RTCPeerConnectionState) => void;
};

export type WhepSession = {
  stream: MediaStream;
  peer: RTCPeerConnection;
  resourceUrl: string;
  destroy: () => Promise<void>;
};

const DEFAULT_ICE_SERVERS: RTCIceServer[] = [
  { urls: 'stun:stun.l.google.com:19302' },
];

const ICE_FRAGMENT_TYPE = 'application/trickle-ice-sdpfrag';
const SDP_TYPE = 'application/sdp';

export async function connectWhep({
  url,
  iceServers,
  video = true,
  audio = true,
  onStateChange,
}: WhepOptions): Promise<WhepSession> {
  if (typeof RTCPeerConnection === 'undefined') {
    throw new Error('WebRTC is not supported in this browser');
  }

  const pc = new RTCPeerConnection({ iceServers: iceServers ?? DEFAULT_ICE_SERVERS });
  if (video) pc.addTransceiver('video', { direction: 'recvonly' });
  if (audio) pc.addTransceiver('audio', { direction: 'recvonly' });

  const stream = new MediaStream();
  const onTrack = (ev: RTCTrackEvent) => {
    stream.addTrack(ev.track);
  };
  pc.addEventListener('track', onTrack);

  if (onStateChange) {
    pc.addEventListener('connectionstatechange', () => onStateChange(pc.connectionState));
  }

  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);

  const postRes = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': SDP_TYPE },
    body: pc.localDescription?.sdp ?? '',
    credentials: 'include',
  });
  if (!postRes.ok) {
    pc.close();
    throw new Error(`WHEP POST failed: ${postRes.status} ${postRes.statusText}`);
  }
  const resourceUrl = postRes.headers.get('Location');
  if (!resourceUrl) {
    pc.close();
    throw new Error('WHEP response missing Location header');
  }
  const answerSdp = await postRes.text();
  await pc.setRemoteDescription({ type: 'answer', sdp: answerSdp });

  let destroyed = false;
  pc.addEventListener('icecandidate', (ev) => {
    if (destroyed) return;
    const fragment =
      ev.candidate === null
        ? 'a=end-of-candidates\r\n'
        : `a=candidate:${ev.candidate.candidate}\r\n`;
    void trickle(resourceUrl, fragment);
  });

  async function trickle(target: string, fragment: string): Promise<void> {
    if (destroyed) return;
    try {
      const res = await fetch(target, {
        method: 'PATCH',
        headers: { 'Content-Type': ICE_FRAGMENT_TYPE },
        body: fragment,
        credentials: 'include',
      });
      if (!res.ok) {
        // eslint-disable-next-line no-console
        console.warn('[whep] trickle PATCH', res.status, res.statusText);
      }
    } catch (err) {
      // eslint-disable-next-line no-console
      console.warn('[whep] trickle failed', err);
    }
  }

  return {
    stream,
    peer: pc,
    resourceUrl,
    async destroy() {
      if (destroyed) return;
      destroyed = true;
      try {
        await fetch(resourceUrl, { method: 'DELETE', credentials: 'include' });
      } catch (err) {
        // eslint-disable-next-line no-console
        console.warn('[whep] DELETE failed', err);
      }
      pc.removeEventListener('track', onTrack);
      pc.close();
    },
  };
}
