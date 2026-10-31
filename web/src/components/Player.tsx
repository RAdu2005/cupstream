import clsx from 'clsx';
import { Loader2, Maximize, Minimize, Pause, Play, Volume2, VolumeX, X } from 'lucide-react';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import type { Stream } from '@/api/types.gen';
import type { SessionView } from '@/lib/roomSessions';

type Props = {
  stream: Stream;
  /** The stream's session; undefined until the page opened one. */
  session: SessionView | undefined;
  muted: boolean;
  volume: number;
  /** Paused by the viewer. */
  paused: boolean;
  /** A small background tile in the focus layout. */
  compact?: boolean;
  /** Shown over the video, e.g. a badge about the stream's quality. */
  badge?: ReactNode;
  onMute: (muted: boolean) => void;
  onVolume: (volume: number) => void;
  onPause: (paused: boolean) => void;
  onRetry: () => void;
  onClose: () => void;
  /** Clicking the video of a background tile brings it into focus. */
  onFocus?: () => void;
};

/** One watched stream: its video and controls. The session is opened by the room page. */
export function Player({ stream, session, muted, volume, paused, compact, badge, ...on }: Props) {
  const box = useRef<HTMLDivElement>(null);
  const video = useRef<HTMLVideoElement>(null);
  const [frames, setFrames] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const media = session?.media ?? null;

  useEffect(() => {
    const v = video.current;
    if (!v || v.srcObject === media) return;
    v.srcObject = media;
    setFrames(false);
  }, [media]);

  useEffect(() => {
    if (video.current) video.current.volume = volume;
  }, [volume]);

  useEffect(() => {
    const onChange = () => setFullscreen(document.fullscreenElement === box.current);
    document.addEventListener('fullscreenchange', onChange);
    return () => document.removeEventListener('fullscreenchange', onChange);
  }, []);

  function toggleFullscreen() {
    if (document.fullscreenElement) void document.exitFullscreen();
    else void box.current?.requestFullscreen?.();
  }

  const label = `${stream.user.displayName}'s stream`;
  let overlay: ReactNode = null;
  if (stream.state !== 'live') {
    overlay = <p className="text-sm text-muted">{stream.state === 'starting' ? 'Starting…' : 'Reconnecting…'}</p>;
  } else if (session?.status === 'error') {
    overlay = (
      <>
        <p>{session.error}</p>
        <button className="font-semibold text-accent hover:underline" onClick={on.onRetry}>
          Try again
        </button>
      </>
    );
  } else if (paused) {
    overlay = (
      <button className="flex flex-col items-center gap-1 text-sm font-semibold" onClick={() => on.onPause(false)}>
        <Play className="size-8" aria-hidden /> Resume
      </button>
    );
  } else if (!frames) {
    overlay = <Loader2 className="size-6 animate-spin text-muted" aria-label="Connecting" />;
  }

  const iconSize = compact ? 'size-3.5' : 'size-4';
  return (
    <div
      ref={box}
      role="group"
      aria-label={label}
      className={clsx('group relative overflow-hidden rounded-lg bg-black', fullscreen ? 'size-full' : 'aspect-video')}
    >
      <video
        ref={video}
        className={clsx('size-full object-contain', paused && 'opacity-40', on.onFocus && 'cursor-pointer')}
        autoPlay
        playsInline
        muted={muted}
        aria-label={label}
        onPlaying={() => setFrames(true)}
        onClick={on.onFocus}
        onDoubleClick={compact ? undefined : toggleFullscreen}
      />
      {overlay && (
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-2 p-4 text-center text-sm [&>*]:pointer-events-auto">
          {overlay}
        </div>
      )}
      {badge && <div className="absolute top-2 left-2">{badge}</div>}
      {session?.error && session.status === 'ready' && (
        <p role="alert" className="absolute inset-x-2 top-2 rounded bg-black/70 px-2 py-1 text-xs">
          {session.error}
        </p>
      )}
      <div
        className={clsx(
          'absolute inset-x-0 bottom-0 flex items-center gap-1 bg-gradient-to-t from-black/80 to-transparent p-1.5 transition-opacity',
          'opacity-100 sm:opacity-0 sm:group-focus-within:opacity-100 sm:group-hover:opacity-100',
        )}
      >
        <Control label={paused ? 'Resume' : 'Pause'} onClick={() => on.onPause(!paused)}>
          {paused ? <Play className={iconSize} /> : <Pause className={iconSize} />}
        </Control>
        <Control label={muted ? 'Unmute' : 'Mute'} onClick={() => on.onMute(!muted)}>
          {muted ? <VolumeX className={iconSize} /> : <Volume2 className={iconSize} />}
        </Control>
        {!compact && (
          <input
            type="range"
            min={0}
            max={1}
            step={0.05}
            value={muted ? 0 : volume}
            onChange={(e) => on.onVolume(Number(e.target.value))}
            aria-label={`Volume of ${label}`}
            className="w-24 accent-accent"
          />
        )}
        <span className="flex-1 truncate px-1 text-xs font-semibold">{stream.user.displayName}</span>
        {!compact && (
          <Control label={fullscreen ? 'Exit fullscreen' : 'Fullscreen'} onClick={toggleFullscreen}>
            {fullscreen ? <Minimize className={iconSize} /> : <Maximize className={iconSize} />}
          </Control>
        )}
        <Control label="Stop watching" onClick={on.onClose}>
          <X className={iconSize} />
        </Control>
      </div>
    </div>
  );
}

function Control({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return (
    <button className="rounded p-1.5 hover:bg-white/15" aria-label={label} title={label} onClick={onClick}>
      {children}
    </button>
  );
}
