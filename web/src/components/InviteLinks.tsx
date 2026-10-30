import { Copy, Link2 } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import { useCreateInvite, useInvites, useRevokeInvite } from '@/api/invites';
import type { CreateInviteRequest, Invite } from '@/api/types.gen';
import { Button, Input } from './ui';

type Expiry = CreateInviteRequest['expiry'];

const expiryLabel: Record<Expiry, string> = {
  '1h': '1 hour',
  '24h': '24 hours',
  stream: 'Until the stream ends',
  never: 'Never',
};

/** Creating and revoking a link room's invite links (the owner only). */
export function InviteLinks({ slug }: { slug: string }) {
  const invites = useInvites(slug);
  const create = useCreateInvite(slug);
  const revoke = useRevokeInvite(slug);
  const [expiry, setExpiry] = useState<Expiry>('24h');
  const [maxUses, setMaxUses] = useState('');
  const [canStream, setCanStream] = useState(false);
  const [created, setCreated] = useState<{ id: string; url: string } | null>(null);
  const [copied, setCopied] = useState(false);

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      { expiry, maxUses: maxUses ? Number(maxUses) : undefined, canStream },
      {
        onSuccess: (r) => {
          setCreated({ id: r.invite.id, url: r.url });
          setCopied(false);
        },
      },
    );
  }

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted">
        Anyone who opens a link and signs in can watch while it is valid. They are not added to your allowed list, and
        revoking the link removes them.
      </p>
      <form onSubmit={submit} className="grid gap-3 sm:grid-cols-2">
        <label className="space-y-1.5">
          <span className="text-sm font-medium">Expires after</span>
          <select
            className="h-9 w-full rounded-md border border-line bg-bg px-2 text-sm"
            value={expiry}
            onChange={(e) => setExpiry(e.target.value as Expiry)}
          >
            {(Object.keys(expiryLabel) as Expiry[]).map((k) => (
              <option key={k} value={k}>
                {expiryLabel[k]}
              </option>
            ))}
          </select>
        </label>
        <label className="space-y-1.5">
          <span className="text-sm font-medium">Use limit</span>
          <Input
            type="number"
            min={1}
            max={1000}
            placeholder="No limit"
            value={maxUses}
            onChange={(e) => setMaxUses(e.target.value)}
          />
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" className="accent-accent" checked={canStream} onChange={(e) => setCanStream(e.target.checked)} />
          People with this link can also stream
        </label>
        <Button type="submit" disabled={create.isPending} className="sm:justify-self-end">
          <Link2 className="size-4" aria-hidden /> Create link
        </Button>
      </form>
      {create.error && <p className="text-sm text-danger">{create.error.message}</p>}
      {created && (
        <div className="space-y-1.5 rounded-md border border-accent/40 bg-bg p-3">
          <p className="text-sm font-medium">Copy the link now: it is shown only once.</p>
          <div className="flex items-center gap-2">
            <Input readOnly value={created.url} aria-label="New invite link" onFocus={(e) => e.target.select()} />
            <Button
              variant="secondary"
              aria-label="Copy invite link"
              onClick={() => void navigator.clipboard.writeText(created.url).then(() => setCopied(true))}
            >
              <Copy className="size-4" />
            </Button>
          </div>
          {copied && <p className="text-xs text-muted">Copied</p>}
        </div>
      )}

      <div className="space-y-2 border-t border-line pt-4">
        <h3 className="text-sm font-medium">Active links</h3>
        {invites.data?.length === 0 && <p className="text-sm text-muted">No links yet.</p>}
        {revoke.error && <p className="text-sm text-danger">{revoke.error.message}</p>}
        <ul className="divide-y divide-line">
          {invites.data?.map((inv) => (
            <li key={inv.id} className="flex items-center justify-between gap-3 py-2 text-sm">
              <span>
                {describeInvite(inv, Date.now() / 1000)}
                {inv.id === created?.id && <span className="text-muted"> (new)</span>}
              </span>
              <Button variant="ghost" className="h-7 px-2" disabled={revoke.isPending} onClick={() => revoke.mutate(inv.id)}>
                Revoke
              </Button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

/** One line about a link: when it ends, its uses, and whether it allows streaming. */
export function describeInvite(inv: Invite, now: number) {
  const parts = [
    inv.expiresAt ? `Expires in ${formatLeft(inv.expiresAt - now)}` : inv.untilStreamEnd ? 'Until the stream ends' : 'Never expires',
    inv.maxUses ? `${inv.uses} of ${inv.maxUses} uses` : `${inv.uses} ${inv.uses === 1 ? 'use' : 'uses'}`,
  ];
  if (inv.canStream) parts.push('can stream');
  return parts.join(' · ');
}

function formatLeft(seconds: number) {
  if (seconds < 3600) return `${Math.max(1, Math.round(seconds / 60))} min`;
  return `${Math.round(seconds / 3600)} h`;
}
