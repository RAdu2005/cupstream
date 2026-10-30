import { Copy, Lock, Radio, Square, Users, WifiOff } from 'lucide-react';
import { useState } from 'react';
import { Link, useParams } from 'react-router';
import { useMe } from '@/api/auth';
import { ApiError } from '@/api/client';
import { useRoomRealtime } from '@/api/realtime';
import { useEndStream, useRoom } from '@/api/rooms';
import type { Room as RoomData, Stream, ViewerStats } from '@/api/types.gen';
import { ChatPanel } from '@/components/ChatPanel';
import { ManageRoom } from '@/components/ManageRoom';
import { MemberActions } from '@/components/MemberActions';
import { Player } from '@/components/Player';
import { PresenceList } from '@/components/PresenceList';
import { Button, Card } from '@/components/ui';
import { visibilityLabel } from '@/lib/labels';

export function Room() {
  const { slug = '' } = useParams();
  const room = useRoom(slug);
  const { data: me } = useMe();
  const live = useRoomRealtime(slug, room.isSuccess && !room.error);

  if (room.isPending) return null;
  if (room.error) return <RoomRefused error={room.error} />;
  if (live.kicked) return <Kicked />;

  const data = room.data;
  const isOwner = data.kind === 'link' && !!me && data.owner?.id === me.id;
  return (
    <div className="mx-auto w-full max-w-7xl space-y-6 p-4 sm:p-8">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold">{data.owner ? `${data.owner.displayName}'s room` : 'Discord room'}</h1>
          {data.visibility && (
            <p className="flex items-center gap-1.5 text-sm text-muted">
              <Lock className="size-3.5" aria-hidden /> {visibilityLabel[data.visibility]}
            </p>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-3">
          {live.status !== 'open' && (
            <span className="flex items-center gap-1.5 text-sm text-muted" role="status">
              <WifiOff className="size-3.5" aria-hidden /> Reconnecting…
            </span>
          )}
          {data.permissions.stream && <ObsHint slug={data.slug} />}
          {isOwner && <ManageRoom slug={data.slug} />}
        </div>
      </header>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        {data.streams.length === 0 ? (
          <Card className="flex flex-col items-center justify-center gap-2 py-16 text-center">
            <Radio className="size-8 text-muted" aria-hidden />
            <p className="font-semibold">Nobody is live in this room</p>
            <p className="text-sm text-muted">Streams appear here as soon as someone goes live.</p>
          </Card>
        ) : (
          <div
            className={data.streams.length > 1 ? 'grid content-start gap-4 xl:grid-cols-2' : 'grid content-start gap-4'}
          >
            {data.streams.map((s) => (
              <StreamTile key={s.id} stream={s} room={data} viewerStats={live.viewerStats[s.id]} />
            ))}
          </div>
        )}
        <aside className="flex flex-col gap-4">
          <PresenceList
            presence={live.presence}
            live={new Set(data.streams.map((s) => s.user.id))}
            actions={isOwner ? (u) => u.id !== me?.id && <MemberActions slug={data.slug} user={u} /> : undefined}
          />
          <ChatPanel
            messages={live.chat}
            signedIn={!!me}
            canChat={data.permissions.chat}
            canModerate={data.permissions.moderateChat}
            connected={live.status === 'open'}
            error={live.chatError}
            onSend={live.sendChat}
            onDelete={live.deleteChat}
          />
        </aside>
      </div>
    </div>
  );
}

function StreamTile({ stream, room, viewerStats }: { stream: Stream; room: RoomData; viewerStats?: ViewerStats }) {
  const { data: me } = useMe();
  const end = useEndStream(room.slug);
  const canEnd = me?.id === stream.user.id || room.permissions.moderateStreams;

  return (
    <div className="space-y-2">
      {stream.state === 'live' ? (
        <Player streamId={stream.id} label={`${stream.user.displayName}'s stream`} />
      ) : (
        <div className="flex aspect-video items-center justify-center rounded-lg bg-surface text-sm text-muted">
          {stream.state === 'starting' ? 'Starting…' : 'Reconnecting…'}
        </div>
      )}
      <div className="flex items-center justify-between gap-2 text-sm">
        <div className="flex items-center gap-2">
          {stream.state === 'live' && <span className="rounded bg-live px-1.5 text-xs font-bold text-white">LIVE</span>}
          <span className="font-semibold">{stream.user.displayName}</span>
          <span className="text-muted">via {sourceLabel[stream.source]}</span>
        </div>
        <div className="flex items-center gap-3 text-muted">
          <span className="flex items-center gap-1" title="Viewers">
            <Users className="size-3.5" aria-hidden /> {stream.viewers}
          </span>
          {canEnd && (
            <Button
              variant="ghost"
              className="h-7 px-2"
              disabled={end.isPending}
              onClick={() => end.mutate(stream.id)}
              title={me?.id === stream.user.id ? 'Stop streaming' : 'End this stream'}
            >
              <Square className="size-3.5" aria-hidden /> End
            </Button>
          )}
        </div>
      </div>
      {me?.id === stream.user.id && viewerStats && viewerStats.viewers > 0 && (
        <p className="text-xs text-muted">
          Your viewers receive {formatBitrate(viewerStats.bitrate / viewerStats.viewers)} each; worst packet loss{' '}
          {(viewerStats.maxLossRatio * 100).toFixed(1)}%, worst round trip {Math.round(viewerStats.maxRttMs)} ms.
        </p>
      )}
      {me?.id === stream.user.id && stream.source === 'obs' && (stream.keyFrameInterval ?? 0) > slowKeyFrames && (
        <p className="text-sm text-muted">
          Your keyframe interval is about {Math.round(stream.keyFrameInterval ?? 0)} seconds, so new viewers wait that
          long for the picture. Set it to 1–2 seconds in OBS (Settings, Output, Keyframe Interval).
        </p>
      )}
    </div>
  );
}

function formatBitrate(bps: number) {
  return bps >= 1e6 ? `${(bps / 1e6).toFixed(1)} Mbps` : `${Math.round(bps / 1e3)} kbps`;
}

// OBS ignores requests for keyframes, so its interval is what new viewers wait.
const slowKeyFrames = 3;

const sourceLabel: Record<Stream['source'], string> = { app: 'app', web: 'browser', obs: 'OBS' };

function ObsHint({ slug }: { slug: string }) {
  const [copied, setCopied] = useState(false);
  const url = `${window.location.origin}/whip?room=${slug}`;
  return (
    <div className="flex items-center gap-2 rounded-md border border-line bg-surface px-3 py-1.5 text-sm">
      <span className="text-muted">OBS server for this room:</span>
      <code className="text-xs">{url}</code>
      <button
        aria-label="Copy OBS server URL"
        className="text-muted hover:text-fg"
        onClick={() => void navigator.clipboard.writeText(url).then(() => setCopied(true))}
      >
        <Copy className="size-3.5" />
      </button>
      {copied && <span className="text-xs text-muted">Copied</span>}
    </div>
  );
}

function Kicked() {
  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <Card className="max-w-sm space-y-4 text-center">
        <Lock className="mx-auto size-8 text-muted" aria-hidden />
        <p className="font-semibold">The room's owner removed you from this room.</p>
        <Button variant="secondary" className="w-full" onClick={() => window.location.reload()}>
          Try to enter again
        </Button>
      </Card>
    </div>
  );
}

function RoomRefused({ error }: { error: Error }) {
  const code = error instanceof ApiError ? error.code : undefined;
  const status = error instanceof ApiError ? error.status : 0;
  const message =
    status === 404
      ? 'This room does not exist.'
      : code === 'sign_in_required'
        ? 'Sign in to enter this room.'
        : code === 'banned'
          ? 'You were banned from this room.'
          : code === 'not_allowed'
            ? 'This room is invite only. Ask its owner to let you in.'
            : error.message;
  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <Card className="max-w-sm space-y-4 text-center">
        <Lock className="mx-auto size-8 text-muted" aria-hidden />
        <p className="font-semibold">{message}</p>
        {code === 'sign_in_required' && (
          <Link to={`/login?next=${encodeURIComponent(window.location.pathname)}`}>
            <Button className="w-full">Sign in</Button>
          </Link>
        )}
      </Card>
    </div>
  );
}
