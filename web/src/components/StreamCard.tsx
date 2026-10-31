import clsx from 'clsx';
import { Play, Users } from 'lucide-react';
import type { Stream } from '@/api/types.gen';

const sourceLabel: Record<Stream['source'], string> = { app: 'app', web: 'browser', obs: 'OBS' };

/**
 * A live stream the viewer is not watching. Clicking it starts playback,
 * which also satisfies browsers' autoplay rules for sound.
 */
export function StreamCard({
  stream,
  disabled,
  onWatch,
}: {
  stream: Stream;
  /** Why it cannot be watched now, e.g. the viewer's limit is reached. */
  disabled?: string;
  onWatch: () => void;
}) {
  const live = stream.state === 'live';
  return (
    <button
      className={clsx(
        'group w-full overflow-hidden rounded-lg border border-line bg-surface text-left transition-colors',
        live && !disabled ? 'hover:border-accent' : 'cursor-not-allowed opacity-70',
      )}
      disabled={!live || !!disabled}
      title={disabled}
      onClick={onWatch}
      aria-label={`Watch ${stream.user.displayName}'s stream`}
    >
      <div className="relative flex aspect-video items-center justify-center bg-black">
        {stream.thumbnailAt ? (
          // thumbnailAt changes with every upload, so it also refreshes the picture.
          <img
            src={`/api/streams/${stream.id}/thumbnail?v=${stream.thumbnailAt}`}
            alt=""
            className="absolute inset-0 size-full object-cover"
          />
        ) : null}
        {live ? (
          <Play
            className="absolute size-10 rounded-full bg-black/60 p-2.5 opacity-80 transition-opacity group-hover:opacity-100"
            aria-hidden
          />
        ) : (
          <span className="absolute bottom-2 text-sm text-muted">
            {stream.state === 'starting' ? 'Starting…' : 'Reconnecting…'}
          </span>
        )}
        {live && <span className="absolute top-2 left-2 rounded bg-live px-1.5 text-xs font-bold text-white">LIVE</span>}
      </div>
      <div className="flex items-center justify-between gap-2 px-3 py-2 text-sm">
        <span className="truncate">
          <span className="font-semibold">{stream.user.displayName}</span>{' '}
          <span className="text-muted">via {sourceLabel[stream.source]}</span>
        </span>
        <span className="flex shrink-0 items-center gap-1 text-muted" title="Viewers">
          <Users className="size-3.5" aria-hidden /> {stream.viewers}
        </span>
      </div>
    </button>
  );
}
