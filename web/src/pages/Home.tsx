import { Radio } from 'lucide-react';
import { Link } from 'react-router';
import { useMe } from '@/api/auth';
import { Card } from '@/components/ui';

export function Home() {
  const { data: user, isPending } = useMe();
  if (isPending) return null;

  return (
    <div className="mx-auto w-full max-w-3xl space-y-6 p-4 sm:p-8">
      {user ? (
        <h1 className="text-2xl font-bold">Welcome, {user.displayName}</h1>
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
      <Card className="flex flex-col items-center gap-2 py-12 text-center">
        <Radio className="size-8 text-muted" aria-hidden />
        <p className="font-semibold">Nobody is live right now</p>
        <p className="text-sm text-muted">Streams from your friends and rooms will show up here.</p>
      </Card>
    </div>
  );
}
