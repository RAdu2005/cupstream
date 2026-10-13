import { Radio, Settings } from 'lucide-react';
import { Link } from 'react-router';
import { useAccount } from '@/api/account';
import { useMe } from '@/api/auth';
import { Button, Card } from '@/components/ui';

export function Home() {
  const { data: user, isPending } = useMe();
  if (isPending) return null;

  return (
    <div className="mx-auto w-full max-w-3xl space-y-6 p-4 sm:p-8">
      {user ? (
        <SignedIn name={user.displayName} />
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

function SignedIn({ name }: { name: string }) {
  const account = useAccount();
  return (
    <>
      <h1 className="text-2xl font-bold">Welcome, {name}</h1>
      <Card className="flex flex-col items-start gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <Radio className="size-6 text-accent" aria-hidden />
          <div>
            <p className="font-semibold">Your link room</p>
            <p className="text-sm text-muted">Stream there from OBS and share its link with the people you allow in.</p>
          </div>
        </div>
        <div className="flex gap-2">
          {account.data && (
            <Link to={`/r/${account.data.roomSlug}`}>
              <Button>Open room</Button>
            </Link>
          )}
          <Link to="/settings">
            <Button variant="secondary">
              <Settings className="size-4" aria-hidden /> Settings
            </Button>
          </Link>
        </div>
      </Card>
    </>
  );
}
