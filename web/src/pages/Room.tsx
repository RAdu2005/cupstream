import { Copy, Lock, WifiOff } from 'lucide-react';
import { useState } from 'react';
import { Link, useParams } from 'react-router';
import { useMe } from '@/api/auth';
import { ApiError } from '@/api/client';
import { useRoomRealtime } from '@/api/realtime';
import { useRoom } from '@/api/rooms';
import { ChatPanel } from '@/components/ChatPanel';
import { ManageRoom } from '@/components/ManageRoom';
import { MemberActions } from '@/components/MemberActions';
import { DiscordChat } from '@/components/discord/DiscordChat';
import { DiscordJoinPrompt, discordRefusals } from '@/components/discord/DiscordJoinPrompt';
import { DiscordRoomHeading } from '@/components/discord/DiscordRoomHeading';
import { PresenceList } from '@/components/PresenceList';
import { RoomStreams } from '@/components/RoomStreams';
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
          {data.owner ? (
            <h1 className="text-2xl font-bold">{`${data.owner.displayName}'s room`}</h1>
          ) : (
            <DiscordRoomHeading room={data} />
          )}
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
        <RoomStreams key={data.slug} room={data} viewerStats={live.viewerStats} />
        <aside className="flex flex-col gap-4">
          <PresenceList
            presence={live.presence}
            live={new Set(data.streams.map((s) => s.user.id))}
            actions={isOwner ? (u) => u.id !== me?.id && <MemberActions slug={data.slug} user={u} /> : undefined}
          />
          <DiscordChat room={data} count={live.chat.length}>
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
          </DiscordChat>
        </aside>
      </div>
    </div>
  );
}

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
  if (code && discordRefusals.has(code)) return <DiscordJoinPrompt code={code} />;
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
