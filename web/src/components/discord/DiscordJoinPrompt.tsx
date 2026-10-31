import { useQueryClient } from '@tanstack/react-query';
import { ExternalLink, Headphones, LoaderCircle } from 'lucide-react';
import { useEffect } from 'react';
import { Link, useParams } from 'react-router';
import { channelLabel, useDiscordChannel } from '@/api/discord';
import { roomKey } from '@/api/rooms';
import { Button, Card } from '@/components/ui';

/** Refusal codes of Discord rooms that DiscordJoinPrompt explains. */
export const discordRefusals = new Set(['join_voice_channel', 'discord_required', 'discord_reconnecting']);

/** How often the room is tried again while waiting for the visitor to join voice. */
const RETRY_MS = 3000;

/**
 * Shown instead of a Discord room to signed-in people it refused: they need
 * a linked Discord account and to be in the voice channel. The room is
 * tried again every few seconds, so they enter as soon as they join.
 */
export function DiscordJoinPrompt({ code }: { code: string }) {
  const { slug = '' } = useParams();
  const qc = useQueryClient();
  const linked = code !== 'discord_required';
  const channel = useDiscordChannel(slug, linked);

  useEffect(() => {
    if (!linked) return;
    const t = setInterval(() => void qc.invalidateQueries({ queryKey: roomKey(slug), exact: true }), RETRY_MS);
    return () => clearInterval(t);
  }, [linked, slug, qc]);

  const c = channel.data;
  const where = c?.guildName ? (
    <>
      <span className="font-semibold text-fg">{channelLabel(c)}</span> in{' '}
      <span className="font-semibold text-fg">{c.guildName}</span>
    </>
  ) : (
    'the voice channel'
  );

  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <Card className="max-w-sm space-y-4 text-center">
        {c?.guildIconUrl ? (
          <img src={c.guildIconUrl} alt="" className="mx-auto size-12 rounded-full" />
        ) : (
          <Headphones className="mx-auto size-8 text-muted" aria-hidden />
        )}
        {code === 'discord_required' && (
          <>
            <p className="font-semibold">This room belongs to a Discord voice channel</p>
            <p className="text-sm text-muted">Link your Discord account to watch here.</p>
            <Link to="/settings" className="block">
              <Button className="w-full">Link Discord</Button>
            </Link>
          </>
        )}
        {code === 'join_voice_channel' && (
          <>
            <p className="font-semibold">Join the voice channel to watch</p>
            <p className="text-sm text-muted">
              Only people in {where} can watch. You enter as soon as you join.
            </p>
            {c && (
              <a href={c.url} target="_blank" rel="noreferrer" className="block">
                <Button className="w-full">
                  <ExternalLink className="size-4" aria-hidden /> Open in Discord
                </Button>
              </a>
            )}
          </>
        )}
        {code === 'discord_reconnecting' && (
          <p className="flex items-center justify-center gap-2 font-semibold" role="status">
            <LoaderCircle className="size-4 animate-spin" aria-hidden /> Reconnecting to Discord…
          </p>
        )}
      </Card>
    </div>
  );
}
