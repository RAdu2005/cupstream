import { useState, type FormEvent } from 'react';
import { Link } from 'react-router';
import { useAdminEndStream, useAdminOverview, useBan, useUnban } from '@/api/admin';
import { ApiError } from '@/api/client';
import type { AdminStream, BannedUser } from '@/api/types.gen';
import { Button, Card, Input } from '@/components/ui';

function mbps(bits: number) {
  return `${(bits / 1e6).toFixed(1)} Mbit/s`;
}

/** The admin page (overview 9.4): live streams, viewers and bandwidth, the kill switch and bans. */
export function Admin() {
  const overview = useAdminOverview();

  if (overview.error instanceof ApiError && (overview.error.status === 401 || overview.error.status === 403)) {
    return <p className="p-8 text-center text-muted">This page is for admins.</p>;
  }
  if (!overview.data) return null;
  const o = overview.data;

  return (
    <div className="mx-auto w-full max-w-5xl space-y-6 p-4 sm:p-8">
      <h1 className="text-2xl font-bold">Admin</h1>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Stat label="Live streams" value={String(o.streams.length)} />
        <Stat label="Viewers" value={String(o.viewers)} />
        <Stat label="Incoming" value={mbps(o.incoming)} />
        <Stat label="Outgoing" value={mbps(o.outgoing)} />
      </div>
      <Streams streams={o.streams} />
      <Bans banned={o.banned} />
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card className="space-y-1">
      <div className="text-sm text-muted">{label}</div>
      <div className="text-xl font-bold tabular-nums">{value}</div>
    </Card>
  );
}

function Streams({ streams }: { streams: AdminStream[] }) {
  const end = useAdminEndStream();
  return (
    <Card className="space-y-3">
      <h2 className="font-bold">Live streams</h2>
      {streams.length === 0 ? (
        <p className="text-sm text-muted">Nobody is live.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-sm">
            <thead className="text-muted">
              <tr>
                <th className="py-1 pr-3 font-medium">Streamer</th>
                <th className="py-1 pr-3 font-medium">Room</th>
                <th className="py-1 pr-3 font-medium">Source</th>
                <th className="py-1 pr-3 font-medium">State</th>
                <th className="py-1 pr-3 text-right font-medium">Viewers</th>
                <th className="py-1 pr-3 text-right font-medium">In</th>
                <th className="py-1 pr-3 text-right font-medium">Out</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {streams.map(({ stream, roomSlug, outgoing }) => (
                <tr key={stream.id} className="border-t border-line">
                  <td className="py-2 pr-3">
                    {stream.user.displayName} <span className="text-muted">@{stream.user.handle}</span>
                  </td>
                  <td className="py-2 pr-3">
                    <Link to={`/r/${roomSlug}`} className="text-accent hover:underline">
                      {roomSlug}
                    </Link>
                  </td>
                  <td className="py-2 pr-3">{stream.source}</td>
                  <td className="py-2 pr-3">{stream.state}</td>
                  <td className="py-2 pr-3 text-right tabular-nums">{stream.viewers}</td>
                  <td className="py-2 pr-3 text-right tabular-nums">{mbps(stream.bitrate)}</td>
                  <td className="py-2 pr-3 text-right tabular-nums">{mbps(outgoing)}</td>
                  <td className="py-2 text-right">
                    <Button
                      variant="secondary"
                      disabled={end.isPending}
                      onClick={() => {
                        if (window.confirm(`End ${stream.user.displayName}'s stream?`)) end.mutate(stream.id);
                      }}
                    >
                      End
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {end.error && <p className="text-sm text-danger">{end.error.message}</p>}
    </Card>
  );
}

function Bans({ banned }: { banned: BannedUser[] }) {
  const [handle, setHandle] = useState('');
  const ban = useBan();
  const unban = useUnban();

  function submit(e: FormEvent) {
    e.preventDefault();
    const h = handle.trim().replace(/^@/, '');
    if (!h || !window.confirm(`Ban @${h} from CupStream? They are signed out everywhere and their streams end.`)) return;
    ban.mutate(h, { onSuccess: () => setHandle('') });
  }

  return (
    <Card className="space-y-3">
      <div className="space-y-1">
        <h2 className="font-bold">Bans</h2>
        <p className="text-sm text-muted">
          A banned user is signed out everywhere, their streams end, and they cannot sign in or stream. They can still
          watch rooms open to everyone without signing in.
        </p>
      </div>
      <form onSubmit={submit} className="flex gap-2">
        <Input aria-label="Handle" placeholder="handle" value={handle} onChange={(e) => setHandle(e.target.value)} />
        <Button type="submit" disabled={ban.isPending || !handle.trim()}>
          Ban
        </Button>
      </form>
      {ban.error && <p className="text-sm text-danger">{ban.error.message}</p>}
      {banned.length > 0 && (
        <ul className="divide-y divide-line text-sm">
          {banned.map(({ user, bannedAt }) => (
            <li key={user.id} className="flex items-center justify-between gap-3 py-2">
              <span>
                {user.displayName} <span className="text-muted">@{user.handle}</span>
                <span className="ml-2 text-muted">since {new Date(bannedAt * 1000).toLocaleString()}</span>
              </span>
              <Button variant="ghost" disabled={unban.isPending} onClick={() => unban.mutate(user.handle)}>
                Lift ban
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
