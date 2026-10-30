import { Check, UserPlus } from 'lucide-react';
import { useAccount } from '@/api/account';
import { useFriends } from '@/api/friends';
import { useAllowed, useSetAllowed } from '@/api/rooms';
import type { User } from '@/api/types.gen';
import { Button } from './ui';

/**
 * One click to let a friend into the user's link room: a button that adds
 * them to its allowed list, or says they already are on it.
 */
export function AllowInRoomButton({ user }: { user: User }) {
  const account = useAccount();
  const slug = account.data?.roomSlug ?? '';
  const allowed = useAllowedIn(slug);
  const allow = useSetAllowed(slug);
  if (!slug || !allowed.data) return null;

  if (allowed.data.has(user.id)) {
    return (
      <span className="flex items-center gap-1 text-xs text-muted" title="On your room's allowed list">
        <Check className="size-3.5" aria-hidden /> Allowed in your room
      </span>
    );
  }
  return (
    <Button
      variant="secondary"
      className="h-7 px-2 text-xs"
      disabled={allow.isPending}
      title={allow.error?.message}
      onClick={() => allow.mutate({ handle: user.handle, canStream: false })}
    >
      <UserPlus className="size-3.5" aria-hidden /> Allow in your room
    </Button>
  );
}

function useAllowedIn(slug: string) {
  const allowed = useAllowed(slug, slug !== '');
  return { ...allowed, data: allowed.data && new Set(allowed.data.map((a) => a.user.id)) };
}

/**
 * The friends who are not on a room's allowed list yet, each with a button
 * to allow them: the friends part of the allowed-list picker.
 */
export function AllowFriends({ slug }: { slug: string }) {
  const friends = useFriends();
  const allowed = useAllowedIn(slug);
  const allow = useSetAllowed(slug);
  const isAllowed = allowed.data;
  if (!friends.data || !isAllowed) return null;
  const candidates = friends.data.friends.filter((f) => !isAllowed.has(f.user.id));
  if (candidates.length === 0) return null;

  return (
    <div className="space-y-1.5">
      <p className="text-sm text-muted">Your friends</p>
      <ul className="flex flex-wrap gap-2">
        {candidates.map(({ user }) => (
          <li key={user.id}>
            <Button
              variant="secondary"
              className="h-8 px-3 text-xs"
              disabled={allow.isPending}
              aria-label={`Allow ${user.displayName}`}
              onClick={() => allow.mutate({ handle: user.handle, canStream: false })}
            >
              <UserPlus className="size-3.5" aria-hidden /> {user.displayName}
            </Button>
          </li>
        ))}
      </ul>
    </div>
  );
}
