import { Loader2, Mail } from 'lucide-react';
import { useEffect, useRef, type ReactNode } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import { useMe } from '@/api/auth';
import { ApiError } from '@/api/client';
import { useInvitePreview, useRedeemInvite } from '@/api/invites';
import { Button, Card } from '@/components/ui';

/** /i/:token: an invite link. Signed-in visitors join at once; others sign in first. */
export function Invite() {
  const { token = '' } = useParams();
  const me = useMe();
  const preview = useInvitePreview(token);
  const redeem = useRedeemInvite();
  const navigate = useNavigate();
  const { mutate } = redeem;

  const signedIn = !!me.data;
  const redeemed = useRef('');
  useEffect(() => {
    if (signedIn && preview.isSuccess && redeemed.current !== token) {
      redeemed.current = token;
      mutate(token, { onSuccess: (r) => navigate(`/r/${r.roomSlug}`, { replace: true }) });
    }
  }, [signedIn, preview.isSuccess, token, mutate, navigate]);

  const error = preview.error ?? redeem.error;
  if (error) return <InviteMessage message={inviteError(error)} />;
  if (me.isPending || !preview.data || signedIn) {
    return (
      <div className="flex flex-1 items-center justify-center">
        <Loader2 className="size-6 animate-spin text-muted" aria-label="Joining" />
      </div>
    );
  }
  const owner = preview.data.owner;
  return (
    <InviteMessage message={`${owner.displayName} invited you to their room.`}>
      <p className="text-sm text-muted">
        Sign in to {preview.data.canStream ? 'watch and stream' : 'watch'}. You can use Google or Discord.
      </p>
      <Link to={`/login?next=${encodeURIComponent(`/i/${token}`)}`}>
        <Button className="w-full">Sign in</Button>
      </Link>
    </InviteMessage>
  );
}

function inviteError(error: Error) {
  const code = error instanceof ApiError ? error.code : undefined;
  if (code === 'invite_invalid') return 'This invite link has expired, was revoked or has been used up.';
  if (code === 'banned') return 'You were banned from this room.';
  return error.message;
}

function InviteMessage({ message, children }: { message: string; children?: ReactNode }) {
  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <Card className="max-w-sm space-y-4 text-center">
        <Mail className="mx-auto size-8 text-muted" aria-hidden />
        <p className="font-semibold">{message}</p>
        {children}
      </Card>
    </div>
  );
}
