import clsx from 'clsx';
import { AlertTriangle, LayoutGrid, Radio, Square, Users, View } from 'lucide-react';
import { useEffect, useMemo, useReducer, useState, useSyncExternalStore } from 'react';
import { useMe } from '@/api/auth';
import { useEndStream } from '@/api/rooms';
import type { Room, Stream, ViewerStats } from '@/api/types.gen';
import { formatMbps, overCapacity } from '@/lib/bandwidth';
import { RoomSessions, type SessionView } from '@/lib/roomSessions';
import {
  canPlayMore,
  initialView,
  isMuted,
  MAX_WATCHING,
  playingCount,
  reduceView,
  wants,
  type Layout,
  type ViewAction,
  type ViewState,
} from '@/lib/roomView';
import { useBandwidth } from '@/lib/useBandwidth';
import { Dialog } from './Dialog';
import { MoveStreamButton } from './discord/MoveStreamDialog';
import { Player } from './Player';
import { StreamCard } from './StreamCard';
import { Button, Card } from './ui';

const noSessions = { subscribe: () => () => undefined, getSnapshot: () => ({}) as Record<string, SessionView> };

/** Whether the page is hidden (another tab, a minimized window). */
export function useDocumentHidden() {
  return useSyncExternalStore(
    (fn) => {
      document.addEventListener('visibilitychange', fn);
      return () => document.removeEventListener('visibilitychange', fn);
    },
    () => document.visibilityState === 'hidden',
  );
}

/**
 * The room's streams: cards for the live ones, and the ones being watched in
 * a focus or grid layout. Every live stream has a pre-warmed, paused
 * session, so starting one only waits for its next key frame.
 */
export function RoomStreams({ room, viewerStats }: { room: Room; viewerStats: Record<string, ViewerStats> }) {
  const [view, dispatch] = useReducer(reduceView, initialView);
  const hidden = useDocumentHidden();
  const [sessions, setSessions] = useState<RoomSessions | null>(null);
  useEffect(() => {
    const s = new RoomSessions();
    setSessions(s);
    return () => s.close();
  }, []);
  const store = sessions ?? noSessions;
  const snapshot = useSyncExternalStore(store.subscribe, store.getSnapshot);

  const streams = room.streams;
  const ids = useMemo(() => streams.map((s) => s.id), [streams]);
  const idsKey = ids.join(',');
  useEffect(() => dispatch({ type: 'streams', ids: idsKey ? idsKey.split(',') : [] }), [idsKey]);
  const wanted = useMemo(() => wants(view, ids, hidden), [view, ids, hidden]);
  useEffect(() => sessions?.update(streams, wanted), [sessions, streams, wanted]);
  const bw = useBandwidth(
    room.slug,
    sessions,
    ids.filter((id) => wanted[id] === 'playing'),
  );
  const [warning, setWarning] = useState<{ stream: Stream; needBps: number; capacityBps: number } | null>(null);

  if (streams.length === 0) {
    return (
      <Card className="flex flex-col items-center justify-center gap-2 py-16 text-center">
        <Radio className="size-8 text-muted" aria-hidden />
        <p className="font-semibold">Nobody is live in this room</p>
        <p className="text-sm text-muted">Streams appear here as soon as someone goes live.</p>
      </Card>
    );
  }

  const byId = new Map(streams.map((s) => [s.id, s]));
  const watched = view.watched.flatMap((id) => byId.get(id) ?? []);
  const others = streams.filter((s) => !view.watched.includes(s.id));
  const full = !canPlayMore(view);
  const playingNow = watched.filter((s) => !view.paused[s.id]);

  /**
   * Opens a stream, first warning the viewer if their connection would not
   * keep up. The first time they open a second stream, their connection is
   * measured with a short download test.
   */
  async function watch(s: Stream) {
    if (playingNow.length === 0) {
      dispatch({ type: 'watch', id: s.id });
      return;
    }
    const capacityBps = await bw.ensureCapacity();
    const playingBps = playingNow.reduce((sum, p) => sum + p.bitrate, 0);
    if (capacityBps !== null && overCapacity(capacityBps, playingBps, s.bitrate)) {
      setWarning({ stream: s, needBps: playingBps + s.bitrate, capacityBps });
      return;
    }
    dispatch({ type: 'watch', id: s.id });
  }

  function pauseBackground() {
    const keep = view.focused;
    for (const s of watched) if (s.id !== keep) dispatch({ type: 'pause', id: s.id, paused: true });
  }

  const tile = (s: Stream, compact: boolean) => (
    <Player
      stream={s}
      session={snapshot[s.id]}
      muted={isMuted(view, s.id)}
      volume={view.volume[s.id] ?? 1}
      paused={!!view.paused[s.id]}
      compact={compact}
      badge={bw.verdict.struggling.includes(s.id) ? <StrugglingBadge stream={s} /> : undefined}
      onMute={(muted) => dispatch({ type: 'mute', id: s.id, muted })}
      onVolume={(volume) => dispatch({ type: 'volume', id: s.id, volume })}
      onPause={(paused) => dispatch({ type: 'pause', id: s.id, paused })}
      onRetry={() => sessions?.retry(s.id)}
      onClose={() => dispatch({ type: 'close', id: s.id })}
      onFocus={compact ? () => dispatch({ type: 'focus', id: s.id }) : undefined}
    />
  );

  // One grid for both layouts, arranged with CSS only: re-creating a tile
  // would restart its video.
  const focusedId = (watched.find((s) => s.id === view.focused) ?? watched[0])?.id;
  const focusLayout = view.layout === 'focus';
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {watched.length > 1 && <Toolbar view={view} dispatch={dispatch} />}
      {bw.verdict.limited && (
        <div role="alert" className="flex flex-wrap items-center gap-3 rounded-lg border border-danger/50 bg-danger/10 px-4 py-3 text-sm">
          <AlertTriangle className="size-5 shrink-0 text-danger" aria-hidden />
          <p className="min-w-0 flex-1">
            Your connection cannot keep up with {playingNow.length} streams. Pause the ones in the background (they
            resume almost instantly) or close one.
          </p>
          {playingNow.length > 1 && (
            <Button variant="secondary" onClick={pauseBackground}>
              Pause background streams
            </Button>
          )}
        </div>
      )}
      {watched.length > 0 && (
        <div className="grid grid-cols-6 gap-3">
          {watched.map((s) => {
            const main = !focusLayout || s.id === focusedId;
            return (
              <div
                key={s.id}
                className={clsx(
                  'min-w-0 space-y-2',
                  focusLayout
                    ? main
                      ? 'order-first col-span-6'
                      : 'col-span-3 sm:col-span-2'
                    : watched.length === 1
                      ? 'col-span-6'
                      : 'col-span-6 sm:col-span-3',
                )}
                onClickCapture={focusLayout ? undefined : () => dispatch({ type: 'focus', id: s.id })}
              >
                {tile(s, !main)}
                {main && <StreamInfo stream={s} room={room} viewerStats={viewerStats[s.id]} />}
              </div>
            );
          })}
        </div>
      )}
      {others.length > 0 && (
        <section aria-label="Live in this room" className="space-y-2">
          <h2 className="text-sm font-semibold text-muted">
            {watched.length === 0 ? 'Choose a stream to watch' : 'Also live in this room'}
          </h2>
          {full && (
            <p className="text-sm text-muted" role="status">
              You can watch up to {MAX_WATCHING} streams at once. Pause or close one to watch another.
            </p>
          )}
          {bw.testing && (
            <p className="text-sm text-muted" role="status">
              Checking your connection…
            </p>
          )}
          <div className={clsx('grid gap-3', watched.length === 0 ? 'sm:grid-cols-2' : 'grid-cols-2 sm:grid-cols-3')}>
            {others.map((s) => (
              <StreamCard
                key={s.id}
                stream={s}
                disabled={full ? `You can watch up to ${MAX_WATCHING} streams at once` : undefined}
                onWatch={() => void watch(s)}
              />
            ))}
          </div>
        </section>
      )}
      {warning && (
        <Dialog title="Your connection may not keep up" onClose={() => setWarning(null)}>
          <p className="text-sm">
            Watching {playingNow.length + 1} streams needs about {formatMbps(warning.needBps)}, and your connection
            managed about {formatMbps(warning.capacityBps)}. The streams may stutter.
          </p>
          <div className="flex flex-wrap justify-end gap-2">
            <Button variant="ghost" onClick={() => setWarning(null)}>
              Cancel
            </Button>
            <Button
              variant="secondary"
              onClick={() => {
                for (const p of playingNow) dispatch({ type: 'pause', id: p.id, paused: true });
                dispatch({ type: 'watch', id: warning.stream.id });
                setWarning(null);
              }}
            >
              Pause the others
            </Button>
            <Button
              onClick={() => {
                dispatch({ type: 'watch', id: warning.stream.id });
                setWarning(null);
              }}
            >
              Watch anyway
            </Button>
          </div>
        </Dialog>
      )}
    </div>
  );
}

/**
 * A stream that struggles while the others play fine: the problem is most
 * likely its sender's connection, which the server confirms when it loses
 * packets from them.
 */
function StrugglingBadge({ stream }: { stream: Stream }) {
  const sender = stream.lossRatio >= 0.02;
  return (
    <span
      className="flex items-center gap-1 rounded bg-black/70 px-1.5 py-0.5 text-xs font-semibold text-amber-300"
      title={sender ? `${stream.user.displayName}'s connection to the server is losing packets.` : undefined}
    >
      <AlertTriangle className="size-3" aria-hidden />
      {sender ? `${stream.user.displayName}'s connection is unstable` : 'This stream is stuttering'}
    </span>
  );
}

function Toolbar({ view, dispatch }: { view: ViewState; dispatch: (a: ViewAction) => void }) {
  const option = (layout: Layout, label: string, Icon: typeof View) => (
    <button
      role="radio"
      aria-checked={view.layout === layout}
      className={clsx(
        'flex items-center gap-1.5 rounded px-2 py-1 text-sm',
        view.layout === layout ? 'bg-raised text-fg' : 'text-muted hover:text-fg',
      )}
      onClick={() => dispatch({ type: 'layout', layout })}
    >
      <Icon className="size-4" aria-hidden /> {label}
    </button>
  );
  return (
    <div className="flex items-center justify-between gap-2">
      <div role="radiogroup" aria-label="Layout" className="flex gap-1 rounded-md border border-line p-0.5">
        {option('focus', 'Focus', View)}
        {option('grid', 'Grid', LayoutGrid)}
      </div>
      <span className="text-sm text-muted">
        Watching {playingCount(view)} of {MAX_WATCHING}
      </span>
    </div>
  );
}

/** Who is streaming, their viewers, and for the streamer, how their viewers receive it. */
function StreamInfo({ stream, room, viewerStats }: { stream: Stream; room: Room; viewerStats?: ViewerStats }) {
  const { data: me } = useMe();
  const end = useEndStream(room.slug);
  const own = me?.id === stream.user.id;
  const canEnd = own || room.permissions.moderateStreams;

  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between gap-2 text-sm">
        <div className="flex min-w-0 items-center gap-2">
          {stream.state === 'live' && <span className="rounded bg-live px-1.5 text-xs font-bold text-white">LIVE</span>}
          <span className="truncate font-semibold">{stream.user.displayName}</span>
          <span className="text-muted">via {sourceLabel[stream.source]}</span>
        </div>
        <div className="flex items-center gap-3 text-muted">
          <span className="flex items-center gap-1" title="Viewers">
            <Users className="size-3.5" aria-hidden /> {stream.viewers}
          </span>
          {room.kind === 'discord' && own && <MoveStreamButton streamId={stream.id} />}
          {canEnd && (
            <Button
              variant="ghost"
              className="h-7 px-2"
              disabled={end.isPending}
              onClick={() => end.mutate(stream.id)}
              title={own ? 'Stop streaming' : 'End this stream'}
            >
              <Square className="size-3.5" aria-hidden /> End
            </Button>
          )}
        </div>
      </div>
      {own && viewerStats && viewerStats.viewers > 0 && (
        <p className="text-xs text-muted">
          Your viewers receive {formatBitrate(viewerStats.bitrate / viewerStats.viewers)} each; worst packet loss{' '}
          {(viewerStats.maxLossRatio * 100).toFixed(1)}%, worst round trip {Math.round(viewerStats.maxRttMs)} ms.
        </p>
      )}
      {own && stream.source === 'obs' && (stream.keyFrameInterval ?? 0) > slowKeyFrames && (
        <p className="text-sm text-muted">
          Your keyframe interval is about {Math.round(stream.keyFrameInterval ?? 0)} seconds, so new viewers wait that
          long for the picture. Set it to 1–2 seconds in OBS (Settings, Output, Keyframe Interval).
        </p>
      )}
    </div>
  );
}

export function formatBitrate(bps: number) {
  return bps >= 1e6 ? `${(bps / 1e6).toFixed(1)} Mbps` : `${Math.round(bps / 1e3)} kbps`;
}

// OBS ignores requests for keyframes, so its interval is what new viewers wait.
const slowKeyFrames = 3;

const sourceLabel: Record<Stream['source'], string> = { app: 'app', web: 'browser', obs: 'OBS' };
