import { CupSoda, LogOut, Settings, Users } from 'lucide-react';
import { Link, Outlet, useNavigate } from 'react-router';
import { useLogout, useMe } from '@/api/auth';
import { useFriends } from '@/api/friends';
import { GoLiveButton } from './GoLiveButton';
import { Notifications } from './Notifications';
import { Button } from './ui';

export function AppShell() {
  return (
    <div className="flex min-h-full flex-col">
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-line bg-surface px-4">
        <Link to="/" className="flex items-center gap-2 font-bold tracking-tight">
          <CupSoda className="size-5 text-accent" aria-hidden />
          CupStream
        </Link>
        <UserMenu />
      </header>
      <main className="flex flex-1 flex-col">
        <Outlet />
      </main>
      <footer className="flex shrink-0 justify-center gap-4 border-t border-line px-4 py-3 text-xs text-muted">
        <Link to="/privacy" className="hover:text-fg">
          Privacy
        </Link>
        <Link to="/terms" className="hover:text-fg">
          Terms
        </Link>
      </footer>
    </div>
  );
}

function UserMenu() {
  const { data: user, isPending } = useMe();
  const logout = useLogout();
  const navigate = useNavigate();

  if (isPending) return null;
  if (!user) {
    return (
      <Button variant="secondary" onClick={() => navigate(`/login?next=${encodeURIComponent(window.location.pathname)}`)}>
        Sign in
      </Button>
    );
  }
  return (
    <div className="flex items-center gap-3">
      <GoLiveButton />
      <span className="flex items-center gap-2 text-sm">
        {user.avatarUrl && <img src={user.avatarUrl} alt="" className="size-6 rounded-full" />}
        <span className="font-semibold">{user.displayName}</span>
        <span className="hidden text-muted sm:inline">@{user.handle}</span>
      </span>
      <FriendsLink />
      <Notifications userId={user.id} />
      <Link to="/settings" aria-label="Settings" title="Settings" className="rounded-md p-2 text-muted hover:bg-raised hover:text-fg">
        <Settings className="size-4" aria-hidden />
      </Link>
      <Button
        variant="ghost"
        aria-label="Sign out"
        title="Sign out"
        disabled={logout.isPending}
        onClick={() => logout.mutate(undefined, { onSuccess: () => navigate('/') })}
        className="px-2"
      >
        <LogOut className="size-4" aria-hidden />
      </Button>
    </div>
  );
}

/** The friends page, with the number of requests waiting for an answer. */
function FriendsLink() {
  const friends = useFriends();
  const waiting = friends.data?.incoming.length ?? 0;
  const label = waiting > 0 ? `Friends, ${waiting} new ${waiting === 1 ? 'request' : 'requests'}` : 'Friends';
  return (
    <Link to="/friends" aria-label={label} title={label} className="relative rounded-md p-2 text-muted hover:bg-raised hover:text-fg">
      <Users className="size-4" aria-hidden />
      {waiting > 0 && (
        <span className="absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-accent-strong px-1 text-[10px] font-bold text-white">
          {waiting > 9 ? '9+' : waiting}
        </span>
      )}
    </Link>
  );
}
