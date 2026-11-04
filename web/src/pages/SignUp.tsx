import { useState, type FormEvent } from 'react';
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router';
import { useCancelSignUp, useHandleCheck, useMe, usePendingSignUp, useSignUp } from '@/api/auth';
import type { PendingSignUp } from '@/api/types.gen';
import { Button, Card, Input } from '@/components/ui';
import { providerLabel } from '@/lib/labels';
import { safeNext } from './Login';

/** A new user, back from the provider, chooses their handle. */
export function SignUp() {
  const me = useMe();
  const pending = usePendingSignUp();
  const [params] = useSearchParams();
  const next = safeNext(params.get('next'));

  if (me.data) return <Navigate to={next} replace />;
  if (me.isPending || pending.isPending) return null;

  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <Card className="w-full max-w-sm space-y-6">
        {pending.data ? (
          <SignUpForm pending={pending.data} next={next} />
        ) : (
          <div className="space-y-3">
            <h1 className="text-xl font-bold">Sign-up expired</h1>
            <p className="text-sm text-muted">It took too long or was interrupted. Please sign in again.</p>
            <Link to={`/login?next=${encodeURIComponent(next)}`} className="text-sm text-accent hover:underline">
              Sign in
            </Link>
          </div>
        )}
      </Card>
    </div>
  );
}

function SignUpForm({ pending, next }: { pending: PendingSignUp; next: string }) {
  const [handle, setHandle] = useState(pending.handle);
  const [name, setName] = useState(pending.displayName || pending.handle);
  const check = useHandleCheck(handle);
  const signUp = useSignUp();
  const cancel = useCancelSignUp();
  const navigate = useNavigate();

  function submit(e: FormEvent) {
    e.preventDefault();
    signUp.mutate({ handle, displayName: name }, { onSuccess: () => navigate(next, { replace: true }) });
  }

  return (
    <form onSubmit={submit} className="space-y-5">
      <div className="space-y-1">
        <h1 className="text-xl font-bold">Choose your handle</h1>
        <p className="flex items-center gap-2 text-sm text-muted">
          {pending.avatarUrl && <img src={pending.avatarUrl} alt="" className="size-5 rounded-full" />}
          Signed in with {providerLabel[pending.provider]} as {pending.username}.
        </p>
      </div>
      <div className="space-y-1.5">
        <label className="block space-y-1.5">
          <span className="text-sm font-medium">Handle</span>
          <Input
            value={handle}
            onChange={(e) => setHandle(e.target.value.toLowerCase())}
            maxLength={32}
            autoComplete="off"
            autoFocus
            required
          />
        </label>
        <p className={`text-xs ${check?.error ? 'text-danger' : 'text-muted'}`}>
          {check?.error ?? `Your room: ${window.location.host}/r/${handle.trim() || '…'}`}
        </p>
      </div>
      <label className="block space-y-1.5">
        <span className="text-sm font-medium">Display name</span>
        <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={48} required />
      </label>
      <p className="text-xs text-muted">
        Friends find you by your handle. You can change it once a week; your room's link changes with it.
      </p>
      {signUp.error && <p className="text-sm text-danger">{signUp.error.message}</p>}
      <div className="flex items-center gap-3">
        <Button type="submit" disabled={signUp.isPending || !handle.trim() || !name.trim() || check?.available === false}>
          Create account
        </Button>
        <Button
          variant="ghost"
          disabled={cancel.isPending}
          onClick={() => cancel.mutate(undefined, { onSettled: () => navigate('/login', { replace: true }) })}
        >
          Cancel
        </Button>
      </div>
    </form>
  );
}
