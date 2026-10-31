import { Headphones, Users } from 'lucide-react';
import { Link } from 'react-router';
import { channelLabel } from '@/api/discord';
import type { HomeRoom } from '@/api/types.gen';
import { Button, Card } from '@/components/ui';

/** The room of the Discord voice channel the user is in, when the bot is in that server. */
export function DiscordVoiceCard({ room }: { room: HomeRoom }) {
  const c = room.discord;
  const live = room.streams.length > 0;
  return (
    <Card className="flex flex-col items-start gap-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex items-center gap-3">
        {c?.guildIconUrl ? (
          <img src={c.guildIconUrl} alt="" className="size-8 rounded-full" />
        ) : (
          <Headphones className="size-6 text-accent" aria-hidden />
        )}
        <div>
          <p className="font-semibold">
            You are in {channelLabel(c) ?? 'a voice channel'}
            {c?.guildName && <span className="font-normal text-muted"> · {c.guildName}</span>}
          </p>
          <p className="flex items-center gap-1.5 text-sm text-muted">
            {live ? (
              <>
                <span className="rounded bg-live px-1.5 text-xs font-bold text-white">LIVE</span>
                {room.streams.map((s) => s.user.displayName).join(', ')}
                <Users className="ml-1 size-3.5" aria-hidden /> {room.streams.reduce((n, s) => n + s.viewers, 0)}
              </>
            ) : room.canStream ? (
              'Everyone in the channel can watch what you stream there.'
            ) : (
              'Streams in the channel show up there.'
            )}
          </p>
        </div>
      </div>
      <Link to={`/r/${room.slug}`}>
        <Button className="whitespace-nowrap">Open room</Button>
      </Link>
    </Card>
  );
}
