import { Link, useNavigate } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { LogOut, Tv } from 'lucide-react';
import { authStatus, logout } from '@/lib/api';
import { Button } from '@/components/ui/Button';
import { cn } from '@/lib/utils';

export function TopBar() {
  const { data: auth } = useQuery({ queryKey: ['auth'], queryFn: authStatus });
  const qc = useQueryClient();
  const nav = useNavigate();

  async function onLogout() {
    await logout();
    qc.invalidateQueries({ queryKey: ['auth'] });
    nav('/login', { replace: true });
  }

  return (
    <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-zinc-800 bg-zinc-950/95 px-4 backdrop-blur">
      <Link to="/" className="flex items-center gap-2 text-zinc-100">
        <Tv className="h-5 w-5 text-brand" />
        <span className="font-bold tracking-tight">cup</span>
        <span className="text-zinc-500 hidden sm:inline">stream</span>
      </Link>
      <nav className="flex items-center gap-2 text-sm">
        <Link
          to="/about"
          className={cn(
            'rounded px-3 py-1.5 text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100',
          )}
        >
          About
        </Link>
        {auth?.authenticated && (
          <span className="hidden md:inline text-zinc-500">
            chatting as <span className="text-zinc-300">{auth.username}</span>
          </span>
        )}
        {auth?.authenticated && (
          <Button variant="ghost" size="sm" onClick={onLogout}>
            <LogOut className="mr-1.5 h-4 w-4" />
            Logout
          </Button>
        )}
      </nav>
    </header>
  );
}
