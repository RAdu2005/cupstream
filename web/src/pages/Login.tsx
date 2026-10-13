import { useState, type FormEvent } from 'react';
import { Navigate, useNavigate, useSearchParams } from 'react-router';
import { useAuthProviders, useDevLogin, useMe } from '@/api/auth';
import type { AuthProvider } from '@/api/types.gen';
import { Button, Card, Input } from '@/components/ui';
import { oauthErrors, providerLabel } from '@/lib/labels';

/** Keeps redirects on this site. */
export function safeNext(next: string | null): string {
  return next && next.startsWith('/') && !next.startsWith('//') ? next : '/';
}

export function Login() {
  const me = useMe();
  const providers = useAuthProviders();
  const [params] = useSearchParams();
  const next = safeNext(params.get('next'));
  const error = params.get('error');

  if (me.data) return <Navigate to={next} replace />;

  const list = providers.data?.providers ?? [];
  const oauth = list.filter((p): p is Exclude<AuthProvider, 'dev'> => p !== 'dev');
  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <Card className="w-full max-w-sm space-y-6">
        <div className="space-y-1">
          <h1 className="text-xl font-bold">Sign in to CupStream</h1>
          <p className="text-sm text-muted">Watch and stream with your friends.</p>
        </div>
        {error && <p className="rounded-md bg-danger/10 p-3 text-sm text-danger">{oauthErrors[error] ?? oauthErrors.internal}</p>}
        {providers.isPending ? null : list.length === 0 ? (
          <p className="text-sm text-muted">No sign-in methods are configured on this server.</p>
        ) : (
          <div className="space-y-3">
            {oauth.map((p) => (
              <a
                key={p}
                href={`/api/auth/${p}/start?next=${encodeURIComponent(next)}`}
                className={
                  'flex h-10 w-full items-center justify-center rounded-md text-sm font-semibold transition-colors ' +
                  (p === 'discord' ? 'bg-[#5865f2] text-white hover:bg-[#4752c4]' : 'bg-white text-black hover:bg-white/90')
                }
              >
                Continue with {providerLabel[p]}
              </a>
            ))}
            {oauth.length > 0 && (
              <p className="text-xs text-muted">
                Sign in with Discord to use CupStream in your Discord voice channels. You can link the other one later.
              </p>
            )}
            {list.includes('dev') && <DevLoginForm next={next} />}
          </div>
        )}
      </Card>
    </div>
  );
}

function DevLoginForm({ next }: { next: string }) {
  const [handle, setHandle] = useState('');
  const login = useDevLogin();
  const navigate = useNavigate();

  function submit(e: FormEvent) {
    e.preventDefault();
    login.mutate({ handle }, { onSuccess: () => navigate(next) });
  }

  return (
    <form onSubmit={submit} className="space-y-3 border-t border-line pt-4">
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
