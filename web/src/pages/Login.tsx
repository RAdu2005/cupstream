import { useState, type FormEvent } from 'react';
import { Navigate } from 'react-router';
import { useAuthProviders, useDevLogin, useMe } from '@/api/auth';
import { Button, Card, Input } from '@/components/ui';

export function Login() {
  const me = useMe();
  const providers = useAuthProviders();

  if (me.data) return <Navigate to="/" replace />;

  const list = providers.data?.providers ?? [];
  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <Card className="w-full max-w-sm space-y-6">
        <div className="space-y-1">
          <h1 className="text-xl font-bold">Sign in to CupStream</h1>
          <p className="text-sm text-muted">Watch and stream with your friends.</p>
        </div>
        {providers.isPending ? null : list.length === 0 ? (
          <p className="text-sm text-muted">No sign-in methods are configured on this server.</p>
        ) : (
          list.includes('dev') && <DevLoginForm />
        )}
      </Card>
    </div>
  );
}

function DevLoginForm() {
  const [handle, setHandle] = useState('');
  const login = useDevLogin();

  function submit(e: FormEvent) {
    e.preventDefault();
    login.mutate({ handle });
  }

  return (
    <form onSubmit={submit} className="space-y-3">
      <label className="block space-y-1.5">
        <span className="text-sm font-medium">Development login</span>
        <Input
          value={handle}
          onChange={(e) => setHandle(e.target.value)}
          placeholder="handle, e.g. alice"
          autoComplete="off"
          required
        />
      </label>
      {login.error && <p className="text-sm text-danger">{login.error.message}</p>}
      <Button type="submit" className="w-full" disabled={login.isPending || handle.trim() === ''}>
        Sign in as {handle.trim() || '…'}
      </Button>
      <p className="text-xs text-muted">Only available on local development servers. No password needed.</p>
    </form>
  );
}
