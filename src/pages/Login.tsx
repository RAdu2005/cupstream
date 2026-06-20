import { useState, type FormEvent } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Tv, AlertCircle } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { login } from '@/lib/api';
import type { AuthStatus } from '@/lib/types';

export function LoginPage() {
  const nav = useNavigate();
  const location = useLocation();
  const qc = useQueryClient();
  const from =
    (location.state as { from?: { pathname: string } } | null)?.from?.pathname ?? '/';

  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');

  const mut = useMutation({
    mutationFn: (vars: { username: string; password: string }) =>
      login(vars.username, vars.password),
    onSuccess: (ok, vars) => {
      if (!ok) return;
      // Update the auth cache immediately so ProtectedRoute doesn't see
      // a stale { authenticated: false } and bounce us back to /login
      // while the refetch from invalidateQueries is in flight.
      qc.setQueryData<AuthStatus>(['auth'], {
        authenticated: true,
        username: vars.username.trim(),
      });
      nav(from, { replace: true });
    },
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!username.trim() || !password) return;
    mut.mutate({ username: username.trim(), password });
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-zinc-950 px-4">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm space-y-4 rounded-lg bg-zinc-900 p-6 shadow-lg border border-zinc-800"
      >
        <div className="flex items-center gap-2">
          <Tv className="h-6 w-6 text-brand" />
          <h1 className="text-xl font-bold text-zinc-100">Cup Stream</h1>
        </div>
        <p className="text-sm text-zinc-400">
          Pick a display name and enter the password to watch.
        </p>

        <div className="space-y-2">
          <label className="text-xs uppercase tracking-wider text-zinc-500">
            Display name
          </label>
          <Input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="e.g. alice"
            maxLength={32}
            required
            autoFocus
            autoComplete="username"
          />
        </div>

        <div className="space-y-2">
          <label className="text-xs uppercase tracking-wider text-zinc-500">
            Password
          </label>
          <Input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="••••••••"
            required
            autoComplete="current-password"
          />
        </div>

        {mut.isError && (
          <div className="flex items-center gap-2 rounded bg-red-950/40 px-3 py-2 text-sm text-red-300">
            <AlertCircle className="h-4 w-4" />
            Wrong password.
          </div>
        )}

        <Button
          type="submit"
          className="w-full"
          disabled={mut.isPending || !username.trim() || !password}
        >
          {mut.isPending ? 'Checking…' : 'Enter'}
        </Button>
      </form>
    </div>
  );
}
