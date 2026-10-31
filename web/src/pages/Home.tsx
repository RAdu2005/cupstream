import { DoorOpen, Radio, Settings, Users } from 'lucide-react';
import { Link } from 'react-router';
import { useMe } from '@/api/auth';
import { useHome } from '@/api/home';
import type { HomeRoom, User } from '@/api/types.gen';
import { Button, Card } from '@/components/ui';

export function Home() {
  const { data: user, isPending } = useMe();
  if (isPending) return null;

  return (
    <div className="mx-auto w-full max-w-3xl space-y-6 p-4 sm:p-8">
      {user ? (
        <SignedIn user={user} />
      ) : (
        <div className="space-y-2">
          <h1 className="text-3xl font-bold">Stream with your friends</h1>
          <p className="text-muted">
            Low-latency screen sharing for your Discord voice channels and your own rooms.{' '}
            <Link to="/login" className="font-semibold text-accent hover:underline">
              Sign in
            </Link>{' '}
            to get started.
          </p>
        </div>
      )}
    </div>
  );
}

function SignedIn({ user }: { user: User }) {
  const home = useHome(true);
  const [own, ...others] = home.data?.rooms ?? [];
  const friendsLive = home.data?.friendsLive ?? [];

  return (
    <>
      <h1 className="text-2xl font-bold">Welcome, {user.displayName}</h1>

      {own && (
        <Card className="space-y-4">
          <div className="flex flex-col items-start gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-center gap-3">
              <Radio className="size-6 text-accent" aria-hidden />
              <div>
                <p className="font-semibold">Your room</p>
                <p className="text-sm text-muted">
                  {own.streams.length > 0
                    ? `Live now: ${names(own.streams.map((s) => s.user.displayName))}`
                    : 'Go live there with the Go live button at the top, and invite people from the room.'}
                </p>
              </div>
            </div>
            <div className="flex gap-2">
              <Link to={`/r/${own.slug}`}>
                <Button className="whitespace-nowrap">Open room</Button>
              </Link>
              <Link to="/settings">
                <Button variant="secondary">
                  <Settings className="size-4" aria-hidden /> Settings
                </Button>
              </Link>
            </div>
          </div>
        </Card>
      )}

      {friendsLive.length > 0 && (
        <section aria-label="Friends live now" className="space-y-2">
          <h2 className="font-semibold">Friends live now</h2>
          <RoomList rooms={friendsLive} />
        </section>
      )}

      <section aria-label="Rooms you can enter" className="space-y-2">
        <h2 className="font-semibold">Rooms you can enter</h2>
        {home.isSuccess && others.length === 0 ? (
          <p className="text-sm text-muted">
            When someone adds you to their room or sends you an invite link, their room appears here.
          </p>
        ) : (
          <RoomList rooms={others} />
        )}
        {home.error && <p className="text-sm text-danger">{home.error.message}</p>}
      </section>
    </>
  );
}

const accessLabel: Record<string, string> = { allowed: 'Allowed', pass: 'Invite link' };

function RoomList({ rooms }: { rooms: HomeRoom[] }) {
  return (
    <ul className="divide-y divide-line overflow-hidden rounded-xl border border-line bg-surface">
      {rooms.map((r) => {
        const live = r.streams.length > 0;
        return (
          <li key={r.slug}>
            <Link to={`/r/${r.slug}`} className="flex items-center gap-3 px-4 py-3 hover:bg-raised">
              <span
                aria-hidden
                className="flex size-8 shrink-0 items-center justify-center rounded-full bg-raised text-sm font-semibold"
              >
                {r.owner?.avatarUrl ? (
                  <img src={r.owner.avatarUrl} alt="" className="size-8 rounded-full" />
                ) : (
                  (r.owner?.displayName ?? 'D').slice(0, 1).toUpperCase()
                )}
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate font-semibold">
                  {r.owner ? `${r.owner.displayName}'s room` : 'Discord room'}
                </span>
                <span className="block truncate text-sm text-muted">
                  {live ? `Live: ${names(r.streams.map((s) => s.user.displayName))}` : 'Nobody is live'}
                  {r.access && accessLabel[r.access] && ` · ${accessLabel[r.access]}`}
                  {r.canStream && ' · you can stream here'}
                </span>
              </span>
              {live ? (
                <span className="flex items-center gap-2 text-sm text-muted">
                  <span className="rounded bg-live px-1.5 text-xs font-bold text-white">LIVE</span>
                  <span className="flex items-center gap-1" title="Viewers">
                    <Users className="size-3.5" aria-hidden /> {r.streams.reduce((n, s) => n + s.viewers, 0)}
                  </span>
                </span>
              ) : (
                <DoorOpen className="size-4 text-muted" aria-hidden />
              )}
            </Link>
          </li>
        );
      })}
    </ul>
  );
}

function names(list: string[]) {
  return list.length <= 2 ? list.join(' and ') : `${list.slice(0, 2).join(', ')} and ${list.length - 2} more`;
}
