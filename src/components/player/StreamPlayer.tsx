import { useEffect, useRef } from 'react';
import { attachHls, type HlsHandle } from '@/lib/hls';
import { cn } from '@/lib/utils';

type Props = {
  src: string;
  className?: string;
  autoPlay?: boolean;
};

export function StreamPlayer({ src, className, autoPlay = true }: Props) {
  const ref = useRef<HTMLVideoElement | null>(null);
  const handleRef = useRef<HlsHandle | null>(null);

  useEffect(() => {
    const video = ref.current;
    if (!video) return;
    try {
      handleRef.current = attachHls(video, src);
      if (autoPlay) video.play().catch(() => {});
    } catch (err) {
      console.error('hls attach failed', err);
    }
    return () => {
      handleRef.current?.destroy();
      handleRef.current = null;
    };
  }, [src, autoPlay]);

  return (
    <video
      ref={ref}
      controls
      playsInline
      autoPlay={autoPlay}
      muted
      className={cn('aspect-video w-full rounded-lg bg-black', className)}
    />
  );
}
