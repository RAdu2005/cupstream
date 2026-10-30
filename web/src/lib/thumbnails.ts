/**
 * Thumbnails of a stream: while live, the sender uploads a small JPEG of
 * what it captures every few seconds (PUT /api/streams/{id}/thumbnail with
 * the publish token); room pages show it on the stream's card.
 */

export const THUMBNAIL_INTERVAL_MS = 5_000;
/** The first one goes out soon after going live. */
const FIRST_DELAY_MS = 1_000;
const MAX_WIDTH = 480;
const MAX_HEIGHT = 270;
const QUALITY = 0.7;

/** Draws the current frame of a playing video into a JPEG of at most 480×270. */
export async function snapshot(video: HTMLVideoElement): Promise<Blob | null> {
  const w = video.videoWidth;
  const h = video.videoHeight;
  if (!w || !h) return null;
  const scale = Math.min(1, MAX_WIDTH / w, MAX_HEIGHT / h);
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.round(w * scale));
  canvas.height = Math.max(1, Math.round(h * scale));
  const ctx = canvas.getContext('2d');
  if (!ctx) return null;
  ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
  return new Promise((resolve) => canvas.toBlob(resolve, 'image/jpeg', QUALITY));
}

/** Uploads a thumbnail; false means the stream is gone and uploads should stop. */
export async function uploadThumbnail(streamId: string, token: string, jpeg: Blob): Promise<boolean> {
  try {
    const res = await fetch(`/api/streams/${streamId}/thumbnail`, {
      method: 'PUT',
      headers: { 'Content-Type': 'image/jpeg', Authorization: `Bearer ${token}` },
      body: jpeg,
    });
    return res.status !== 404 && res.status !== 401;
  } catch {
    return true; // the network; try again next time
  }
}

/**
 * Uploads thumbnails of track until the returned function is called. take
 * and upload are replaceable for tests.
 */
export function startThumbnails(
  track: MediaStreamTrack,
  upload: (jpeg: Blob) => Promise<boolean>,
  take: (video: HTMLVideoElement) => Promise<Blob | null> = snapshot,
): () => void {
  const video = document.createElement('video');
  video.muted = true;
  video.playsInline = true;
  video.srcObject = new MediaStream([track]);
  void video.play?.()?.catch(() => undefined);

  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const tick = async () => {
    if (stopped) return;
    let more = true;
    try {
      const jpeg = await take(video);
      if (jpeg && !stopped) more = await upload(jpeg);
    } catch {
      // a frame that could not be drawn; the next one may
    }
    if (!stopped && more) timer = setTimeout(() => void tick(), THUMBNAIL_INTERVAL_MS);
  };
  timer = setTimeout(() => void tick(), FIRST_DELAY_MS);

  return () => {
    stopped = true;
    clearTimeout(timer);
    video.pause?.();
    video.srcObject = null;
  };
}
