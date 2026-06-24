import { useEffect, useRef, useState } from 'react';
import { connectWhep, type WhepSession } from '@/lib/whep';
import { cn } from '@/lib/utils';

type Props = {
  whepUrl: string;
  className?: string;
  autoPlay?: boolean;
};

type Status = 'connecting' | 'connected' | 'failed' | 'unsupported';

export function StreamPlayer({ whepUrl, className, autoPlay = true }: Props) {
  const ref = useRef<HTMLVideoElement | null>(null);
  const sessionRef = useRef<WhepSession | null>(null);
  const [status, setStatus] = useState<Status>('connecting');

  useEffect(() => {
    const video = ref.current;
    if (!video || !whepUrl) return;

    let cancelled = false;
    setStatus('connecting');

    (async () => {
      try {
        const session = await connectWhep({
          url: whepUrl,
          onStateChange: (s) => {
            if (cancelled) return;
            if (s === 'connected') setStatus('connected');
            else if (s === 'failed' || s === 'closed' || s === 'disconnected') {
              setStatus('failed');
            }
          },
        });
        if (cancelled) {
          await session.destroy();
          return;
        }
        sessionRef.current = session;
        video.srcObject = session.stream;
        if (autoPlay) video.play().catch(() => {});
      } catch (err) {
        // eslint-disable-next-line no-console
        console.error('whep attach failed', err);
        if (typeof RTCPeerConnection === 'undefined') setStatus('unsupported');
        else setStatus('failed');
      }
    })();

    return () => {
      cancelled = true;
      const session = sessionRef.current;
      sessionRef.current = null;
      if (video) {
        video.srcObject = null;
        video.load();
      }
      void session?.destroy();
    };
  }, [whepUrl, autoPlay]);

  return (
    <div className="relative">
      <video
        ref={ref}
        controls
        playsInline
        autoPlay={autoPlay}
        muted
        className={cn('aspect-video w-full rounded-lg bg-black', className)}
      />
      {status === 'connecting' && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center text-sm text-zinc-400">
          Connecting…
        </div>
      )}
      {status === 'failed' && (
        <div className="absolute inset-0 flex items-center justify-center bg-black/70 text-sm text-zinc-300">
          Connection lost. Reconnecting…
        </div>
      )}
      {status === 'unsupported' && (
        <div className="absolute inset-0 flex items-center justify-center bg-black/70 p-4 text-center text-sm text-zinc-300">
          Your browser does not support WebRTC playback.
        </div>
      )}
    </div>
  );
}
