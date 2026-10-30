import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { FakeTrack } from '@/test/fakeMedia';
import { startThumbnails, THUMBNAIL_INTERVAL_MS, uploadThumbnail } from './thumbnails';

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal(
    'MediaStream',
    class {
      constructor(readonly tracks: unknown[]) {}
    },
  );
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue();
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockReturnValue();
});
afterEach(() => vi.restoreAllMocks());

const jpeg = new Blob(['jpeg'], { type: 'image/jpeg' });

it('uploads one soon after going live, then every few seconds, until stopped', async () => {
  const take = vi.fn(async () => jpeg);
  const upload = vi.fn(async () => true);
  const stop = startThumbnails(new FakeTrack('video') as unknown as MediaStreamTrack, upload, take);
  await vi.advanceTimersByTimeAsync(1_000);
  expect(upload).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(THUMBNAIL_INTERVAL_MS * 2);
  expect(upload).toHaveBeenCalledTimes(3);
  stop();
  await vi.advanceTimersByTimeAsync(THUMBNAIL_INTERVAL_MS * 2);
  expect(upload).toHaveBeenCalledTimes(3);
});

it('skips frames it could not draw and stops once the stream is gone', async () => {
  const take = vi.fn<() => Promise<Blob | null>>().mockResolvedValueOnce(null).mockRejectedValueOnce(new Error('tainted')).mockResolvedValue(jpeg);
  const upload = vi.fn(async () => false);
  startThumbnails(new FakeTrack('video') as unknown as MediaStreamTrack, upload, take);
  await vi.advanceTimersByTimeAsync(1_000 + THUMBNAIL_INTERVAL_MS * 2);
  expect(upload).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(THUMBNAIL_INTERVAL_MS * 3);
  expect(take).toHaveBeenCalledTimes(3);
});

it('sends the JPEG with the publish token', async () => {
  const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
  vi.stubGlobal('fetch', fetchMock);
  expect(await uploadThumbnail('abc', 'tok', jpeg)).toBe(true);
  expect(fetchMock).toHaveBeenCalledWith('/api/streams/abc/thumbnail', {
    method: 'PUT',
    headers: { 'Content-Type': 'image/jpeg', Authorization: 'Bearer tok' },
    body: jpeg,
  });
  fetchMock.mockResolvedValueOnce(new Response(null, { status: 429 }));
  expect(await uploadThumbnail('abc', 'tok', jpeg)).toBe(true);
  fetchMock.mockResolvedValueOnce(new Response('{}', { status: 404 }));
  expect(await uploadThumbnail('abc', 'tok', jpeg)).toBe(false);
});
