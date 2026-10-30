import { Headphones } from 'lucide-react';
import { channelLabel } from '@/api/discord';
import type { Room } from '@/api/types.gen';

/** A Discord room's title: its voice channel and server. */
export function DiscordRoomHeading({ room }: { room: Room }) {
  const c = room.discord;
  return (
    <>
      <h1 className="flex items-center gap-2 text-2xl font-bold">
        <Headphones className="size-6 text-accent" aria-hidden />
        {channelLabel(c) ?? 'Discord room'}
      </h1>
      {c?.guildName && <p className="text-sm text-muted">{c.guildName} · for everyone in the voice channel</p>}
    </>
  );
}
