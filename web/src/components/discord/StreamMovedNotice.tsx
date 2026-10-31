import { ArrowRightLeft } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Link } from 'react-router';
import type { Stream } from '@/api/types.gen';
import { realtime } from '@/lib/realtime';

type Moved = { streamId: string; toRoom: string; name?: string };

/**
 * Tells the people in a room that a stream moved to its streamer's link
 * room, where those the streamer let in can follow it.
 */
export function StreamMovedNotice({ slug, streams }: { slug: string; streams: Stream[] }) {
  const [moved, setMoved] = useState<Moved[]>([]);
  useEffect(
    () =>
      realtime.subscribe(slug, (msg) => {
        if (msg.type !== 'streamMoved') return;
        setMoved((m) => [...m.filter((x) => x.streamId !== msg.streamId), { streamId: msg.streamId, toRoom: msg.toRoom }]);
      }),
    [slug],
  );
  // Names come from the streams as they were before the move.
  const [names, setNames] = useState<Record<string, string>>({});
  useEffect(() => {
    setNames((n) => {
      const next = { ...n };
      for (const s of streams) next[s.id] = s.user.displayName;
      return next;
    });
  }, [streams]);

  if (moved.length === 0) return null;
  return (
    <div className="space-y-2">
      {moved.map((m) => (
        <p key={m.streamId} role="status" className="flex flex-wrap items-center gap-2 rounded-md border border-line bg-surface px-3 py-2 text-sm">
          <ArrowRightLeft className="size-4 text-accent" aria-hidden />
          {names[m.streamId] ?? 'A streamer'} moved their stream to their own room.
          <Link to={`/r/${m.toRoom}`} className="font-semibold text-accent hover:underline">
            Follow it
          </Link>
        </p>
      ))}
    </div>
  );
}
