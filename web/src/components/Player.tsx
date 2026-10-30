import { Loader2, Volume2, VolumeX } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { realtime } from '@/lib/realtime';
import { playWhep } from '@/lib/whep';

type Status = 'connecting' | 'playing' | 'error';

/** Plays one stream over WHEP while mounted. */
export function Player({ streamId, label }: { streamId: string; label: string }) {
  const video = useRef<HTMLVideoElement>(null);
  const [status, setStatus] = useState<Status>('connecting');
  const [error, setError] = useState('');
  const [muted, setMuted] = useState(true);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    let close: (() => Promise<void>) | undefined;
    setStatus('connecting');

    // The tab ID lets the server close this session when the tab goes away.
    playWhep(`/api/streams/${streamId}/whep?tab=${realtime.tabId}`, (state) => {
      if (state === 'failed' && !cancelled) {
        setStatus('error');
        setError('The connection to the media server failed.');
      }
    })
      .then((session) => {
        if (cancelled) {
          void session.close();
          return;
        }
        close = session.close;
        if (video.current) video.current.srcObject = session.stream;
      })
      .catch((err: Error) => {
        if (cancelled) return;
        setStatus('error');
        setError(err.message);
      });

    return () => {
      cancelled = true;
      void close?.();
    };
  }, [streamId, attempt]);

  return (
    <div className="group relative aspect-video overflow-hidden rounded-lg bg-black">
      <video
        ref={video}
        className="size-full object-contain"
        autoPlay
        playsInline
        muted={muted}
        aria-label={label}
        onPlaying={() => setStatus('playing')}
      />
      {status === 'connecting' && (
        <div className="absolute inset-0 flex items-center justify-center text-muted">
          <Loader2 className="size-6 animate-spin" aria-label="Connecting" />
        </div>
      )}
      {status === 'error' && (
        <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 p-4 text-center text-sm">
          <p>{error}</p>
          <button className="font-semibold text-accent hover:underline" onClick={() => setAttempt((a) => a + 1)}>
            Try again
          </button>
        </div>
      )}
      {status === 'playing' && (
        <button
          className="absolute right-2 bottom-2 rounded-md bg-black/60 p-2 opacity-80 transition-opacity hover:opacity-100"
          onClick={() => setMuted((m) => !m)}
          aria-label={muted ? 'Unmute' : 'Mute'}
          title={muted ? 'Unmute' : 'Mute'}
        >
          {muted ? <VolumeX className="size-4" /> : <Volume2 className="size-4" />}
        </button>
      )}
    </div>
  );
}
