import { useQueryClient } from '@tanstack/react-query';
import { X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Link } from 'react-router';
import { friendsKey } from '@/api/friends';
import type { Notification } from '@/api/types.gen';
import { realtime } from '@/lib/realtime';

/** How long a notification stays on screen. */
const SHOW_MS = 8000;

type Shown = Notification & { key: string };

/**
 * App-wide realtime messages for the signed-in user: keeps the friends
 * queries current and shows notifications (friend requests, friends going
 * live) wherever they are on the site.
 */
export function Notifications({ userId }: { userId: string }) {
  const qc = useQueryClient();
  const [shown, setShown] = useState<Shown[]>([]);

  useEffect(() => {
    realtime.setIdentity(userId);
    return realtime.listen((msg) => {
      if (msg.type !== 'notification' && msg.type !== 'friendsChanged') return;
      void qc.invalidateQueries({ queryKey: friendsKey });
      if (msg.type === 'notification') {
        const key = crypto.randomUUID();
        setShown((s) => [...s.slice(-2), { ...msg, key }]);
        setTimeout(() => setShown((s) => s.filter((n) => n.key !== key)), SHOW_MS);
      }
    });
  }, [userId, qc]);

  const dismiss = (key: string) => setShown((s) => s.filter((n) => n.key !== key));
  if (shown.length === 0) return null;
  return (
    <div className="pointer-events-none fixed right-4 bottom-4 z-50 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2">
      {shown.map((n) => (
        <div
          key={n.key}
          role="status"
          className="pointer-events-auto flex items-start gap-3 rounded-lg border border-line bg-raised p-3 text-sm shadow-lg"
        >
          {n.user.avatarUrl && <img src={n.user.avatarUrl} alt="" className="size-8 rounded-full" />}
          <div className="min-w-0 flex-1 space-y-1">
            <p>
              <span className="font-semibold">{n.user.displayName}</span> {text[n.kind]}
            </p>
            <Link
              to={n.kind === 'friendLive' && n.roomSlug ? `/r/${n.roomSlug}` : '/friends'}
              onClick={() => dismiss(n.key)}
              className="font-semibold text-accent hover:underline"
            >
              {n.kind === 'friendLive' ? 'Watch' : n.kind === 'friendRequest' ? 'View request' : 'Friends'}
            </Link>
          </div>
          <button aria-label="Dismiss" className="text-muted hover:text-fg" onClick={() => dismiss(n.key)}>
            <X className="size-4" />
          </button>
        </div>
      ))}
    </div>
  );
}

const text: Record<Notification['kind'], string> = {
  friendRequest: 'sent you a friend request.',
  friendAccepted: 'accepted your friend request.',
  friendLive: 'is live.',
};
