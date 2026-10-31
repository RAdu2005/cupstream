import { Timer } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router';
import { useUserEvents } from '@/api/discord';
import { MoveStreamButton } from './MoveStreamDialog';
import type { ServerMessage } from '@/lib/realtime';

type Grace = { roomSlug: string; streamId: string; endsAt: number };

/**
 * The countdown a streamer sees after leaving the voice channel of the
 * Discord room they stream in: rejoining within it keeps the stream going.
 */
export function StreamGraceBanner() {
  const [grace, setGrace] = useState<Grace | null>(null);
  const [now, setNow] = useState(() => Date.now());

  const onMessage = useCallback((msg: ServerMessage) => {
    if (msg.type !== 'streamGrace') return;
    setNow(Date.now());
    setGrace(
      msg.endsInMs > 0 ? { roomSlug: msg.roomSlug, streamId: msg.streamId, endsAt: Date.now() + msg.endsInMs } : null,
    );
  }, []);
  useUserEvents(onMessage);

  useEffect(() => {
    if (!grace) return;
    const t = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(t);
  }, [grace]);

  if (!grace || now > grace.endsAt + 2000) return null;
  const seconds = Math.max(0, Math.ceil((grace.endsAt - now) / 1000));
  return (
    <div role="alert" className="flex flex-wrap items-center justify-center gap-3 bg-live px-4 py-2 text-sm text-white">
      <Timer className="size-4" aria-hidden />
      {seconds > 0 ? (
        <span>
          You left the voice channel: your stream ends in <strong>{seconds} s</strong>. Rejoin it to keep streaming.
        </span>
      ) : (
        <span>Your stream ended because you left the voice channel.</span>
      )}
      {seconds > 0 && (
        <MoveStreamButton streamId={grace.streamId} className="h-7 bg-white/15 px-2 text-white hover:bg-white/25" />
      )}
      <Link to={`/r/${grace.roomSlug}`} className="font-semibold underline">
        Room
      </Link>
    </div>
  );
}
