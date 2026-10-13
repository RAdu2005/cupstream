import { CupSoda, LogOut } from 'lucide-react';
import { Link, Outlet, useNavigate } from 'react-router';
import { useLogout, useMe } from '@/api/auth';
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
      <Button variant="secondary" onClick={() => navigate('/login')}>
        Sign in
      </Button>
    );
  }
  return (
    <div className="flex items-center gap-3">
      <span className="text-sm">
        <span className="font-semibold">{user.displayName}</span>{' '}
        <span className="text-muted">@{user.handle}</span>
      </span>
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
