import { useMutation } from '@tanstack/react-query';
import { useState, type ReactNode } from 'react';
import { Navigate, useLocation, useSearchParams } from 'react-router';
import { api } from '@/api/client';
import { useMe } from '@/api/auth';
import type { AppAuthorizeRequest, AppAuthorizeResponse } from '@/api/types.gen';
import { Button, Card } from '@/components/ui';

/**
 * The app's loopback address, as the server requires it: http://127.0.0.1
 * or http://[::1] with a port. Anything else is not sent anywhere.
 */
export function loopbackRedirect(uri: string | null): URL | null {
  if (!uri) return null;
  try {
    const u = new URL(uri);
    const loopback = u.hostname === '127.0.0.1' || u.hostname === '[::1]';
    return u.protocol === 'http:' && loopback && u.port !== '' && !u.username && !u.hash ? u : null;
  } catch {
    return null;
  }
}

/**
 * /app/authorize: the desktop app opens this page in the system browser to
 * sign in. The signed-in user approves it, and the browser goes back to the
 * app's loopback address with a one-time code.
 */
export function AppAuthorize() {
  const me = useMe();
  const location = useLocation();
  const [params] = useSearchParams();
  const [done, setDone] = useState<'approved' | 'cancelled' | null>(null);
  const redirect = loopbackRedirect(params.get('redirect_uri'));
  const state = params.get('state') ?? '';
  const deviceName = params.get('device_name')?.trim() || 'the CupStream app';

  const approve = useMutation({
    mutationFn: (req: AppAuthorizeRequest) =>
      api<AppAuthorizeResponse>('/api/app/authorize', { method: 'POST', json: req }),
    onSuccess: (res) => {
      setDone('approved');
      window.location.assign(res.redirect);
    },
  });

  if (me.isPending) return null;
  if (!me.data) {
    return <Navigate to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`} replace />;
  }

  const challenge = params.get('code_challenge');
  if (!redirect || !challenge || params.get('code_challenge_method') !== 'S256') {
    return (
      <Page title="This sign-in link is not valid">
        <p className="text-sm text-muted">Start signing in again from the CupStream app.</p>
      </Page>
    );
  }

  function cancel() {
    const back = new URL(redirect!);
    back.searchParams.set('error', 'access_denied');
    back.searchParams.set('state', state);
    setDone('cancelled');
    window.location.assign(back.toString());
  }

  if (done) {
    return (
      <Page title={done === 'approved' ? 'You are signed in' : 'Sign-in cancelled'}>
        <p className="text-sm text-muted">You can close this tab and go back to the CupStream app.</p>
      </Page>
    );
  }

  return (
    <Page title="Sign in to the CupStream app?">
      <p className="text-sm text-muted">
        <span className="font-semibold text-fg">{deviceName}</span> will be signed in as{' '}
        <span className="font-semibold text-fg">{me.data.displayName}</span> (@{me.data.handle}) and can go live for
        you. You can sign it out in Settings at any time.
      </p>
      <p className="text-xs text-muted">Only continue if you just started signing in from the app on this computer.</p>
      {approve.error && <p className="text-sm text-danger">{approve.error.message}</p>}
      <div className="flex gap-3">
        <Button
          className="flex-1"
          disabled={approve.isPending}
          onClick={() =>
            approve.mutate({
              redirectUri: redirect.toString(),
              state,
              codeChallenge: challenge,
              codeChallengeMethod: 'S256',
              deviceName: params.get('device_name') ?? '',
            })
          }
        >
          Sign in
        </Button>
        <Button variant="secondary" className="flex-1" onClick={cancel}>
          Cancel
        </Button>
      </div>
    </Page>
  );
}

function Page({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <Card className="w-full max-w-sm space-y-4">
        <h1 className="text-xl font-bold">{title}</h1>
        {children}
      </Card>
    </div>
  );
}
