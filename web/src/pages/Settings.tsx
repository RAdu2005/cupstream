import { Copy, Monitor, RefreshCw, X } from 'lucide-react';
import { useEffect, useState, type FormEvent, type ReactNode } from 'react';
import { Link, Navigate, useSearchParams } from 'react-router';
import {
  useAccount,
  useGuilds,
  usePendingMerge,
  useRegenerateStreamKey,
  useResolveMerge,
  useRevokeSession,
  useSessions,
  useStreamKey,
  useUnlink,
  useUpdateGuild,
  useUpdateProfile,
  useUpdateSettings,
} from '@/api/account';
import { useAuthProviders, useMe } from '@/api/auth';
import { useAllowed, useBans, useRemoveAllowed, useRoom, useSetAllowed, useUnban, useUpdateRoom } from '@/api/rooms';
import type { Account, AuthProvider, Visibility } from '@/api/types.gen';
import { AllowFriends } from '@/components/AllowFriends';
import { Button, Card, Input } from '@/components/ui';
import { oauthErrors, providerLabel, visibilityLabel } from '@/lib/labels';

export function Settings() {
  const me = useMe();
  const account = useAccount();
  const [params, setParams] = useSearchParams();

  if (me.isPending) return null;
  if (!me.data) return <Navigate to="/login?next=/settings" replace />;
  if (!account.data) return null;

  const notice = params.get('linked')
    ? `${providerLabel[params.get('linked') as AuthProvider] ?? 'Account'} linked.`
    : null;
  const error = params.get('error');
  const acc = account.data;

  return (
    <div className="mx-auto w-full max-w-3xl space-y-6 p-4 sm:p-8">
      <h1 className="text-2xl font-bold">Settings</h1>
      {(notice || error) && (
        <div
          className={`flex items-start justify-between gap-3 rounded-md p-3 text-sm ${error ? 'bg-danger/10 text-danger' : 'bg-accent/10'}`}
        >
          <span>{error ? (oauthErrors[error] ?? oauthErrors.internal) : notice}</span>
          <button aria-label="Dismiss" onClick={() => setParams({})}>
            <X className="size-4" />
          </button>
        </div>
      )}
      <MergePrompt checkPending={params.get('merge') === '1'} onDone={() => setParams({})} />
      <Profile account={acc} />
      <SignInMethods account={acc} />
      <Streaming account={acc} />
      <RoomSettings slug={acc.roomSlug} />
      {acc.identities.some((i) => i.provider === 'discord') && <DiscordServers />}
      <Sessions />
    </div>
  );
}

function Section({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <Card className="space-y-4">
      <div className="space-y-1">
        <h2 className="font-bold">{title}</h2>
        {description && <p className="text-sm text-muted">{description}</p>}
      </div>
      {children}
    </Card>
  );
}

function Profile({ account }: { account: Account }) {
  const [handle, setHandle] = useState(account.user.handle);
  const [name, setName] = useState(account.user.displayName);
  const update = useUpdateProfile();
  const dirty = handle !== account.user.handle || name !== account.user.displayName;

  function submit(e: FormEvent) {
    e.preventDefault();
    update.mutate({ handle, displayName: name });
  }

  return (
    <Section title="Profile">
      <form onSubmit={submit} className="grid gap-3 sm:grid-cols-2">
        <label className="space-y-1.5">
          <span className="text-sm font-medium">Display name</span>
          <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={48} />
        </label>
        <label className="space-y-1.5">
          <span className="text-sm font-medium">Handle</span>
          <Input value={handle} onChange={(e) => setHandle(e.target.value.toLowerCase())} maxLength={32} />
        </label>
        <div className="flex items-center gap-3 sm:col-span-2">
          <Button type="submit" disabled={!dirty || update.isPending}>
            Save
          </Button>
          {update.error && <span className="text-sm text-danger">{update.error.message}</span>}
          {update.isSuccess && !dirty && <span className="text-sm text-muted">Saved</span>}
        </div>
      </form>
    </Section>
  );
}

function SignInMethods({ account }: { account: Account }) {
  const providers = useAuthProviders();
  const unlink = useUnlink();
  const linked = new Map(account.identities.map((i) => [i.provider, i.username]));
  const available = (providers.data?.providers ?? []).filter((p) => p !== 'dev');
  const shown = [...new Set<AuthProvider>([...available, ...account.identities.map((i) => i.provider)])];

  return (
    <Section title="Sign-in methods" description="Link Discord to use CupStream in your Discord voice channels.">
      <ul className="divide-y divide-line">
        {shown.map((p) => (
          <li key={p} className="flex items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0">
            <div className="text-sm">
              <span className="font-semibold">{providerLabel[p]}</span>
              {linked.has(p) && <span className="text-muted"> · {linked.get(p)}</span>}
            </div>
            {linked.has(p) ? (
              <Button
                variant="secondary"
                disabled={linked.size <= 1 || unlink.isPending}
                title={linked.size <= 1 ? 'This is your only way to sign in' : undefined}
                onClick={() => unlink.mutate(p)}
              >
                Unlink
              </Button>
            ) : (
              <a href={`/api/auth/${p}/start?mode=link`}>
                <Button variant="secondary">Link</Button>
              </a>
            )}
          </li>
        ))}
      </ul>
      {unlink.error && <p className="text-sm text-danger">{unlink.error.message}</p>}
    </Section>
  );
}

function MergePrompt({ checkPending, onDone }: { checkPending: boolean; onDone: () => void }) {
  const pending = usePendingMerge(checkPending);
  const resolve = useResolveMerge();
  if (!pending.data) return null;
  const m = pending.data;

  return (
    <Card className="space-y-3 border-accent">
      <h2 className="font-bold">Merge accounts?</h2>
      <p className="text-sm">
        The {providerLabel[m.provider]} account <strong>{m.username}</strong> already belongs to the CupStream account{' '}
        <strong>
          {m.other.displayName} (@{m.other.handle})
        </strong>
        . Merging moves its sign-in methods, room memberships and friends into this account and deletes it, along with its room
        and stream key.
      </p>
      <div className="flex gap-2">
        <Button disabled={resolve.isPending} onClick={() => resolve.mutate(true, { onSuccess: onDone })}>
          Merge into this account
        </Button>
        <Button variant="secondary" disabled={resolve.isPending} onClick={() => resolve.mutate(false, { onSuccess: onDone })}>
          Cancel
        </Button>
      </div>
      {resolve.error && <p className="text-sm text-danger">{resolve.error.message}</p>}
    </Card>
  );
}

function Streaming({ account }: { account: Account }) {
  const settings = useUpdateSettings();
  const key = useStreamKey();
  const regenerate = useRegenerateStreamKey();
  const s = account.settings;

  return (
    <Section title="Streaming" description="Where your streams go when you go live.">
      <fieldset className="space-y-2">
        <legend className="mb-1 text-sm font-medium">Start mode</legend>
        <Radio
          name="start-mode"
          checked={s.startMode === 'discord_auto'}
          onChange={() => settings.mutate({ ...s, startMode: 'discord_auto' })}
          label="Discord (auto)"
          hint="Stream into your Discord voice channel's room when you are in one; otherwise you are offered your link room."
        />
        <Radio
          name="start-mode"
          checked={s.startMode === 'link'}
          onChange={() => settings.mutate({ ...s, startMode: 'link' })}
          label="Link room"
          hint="Always stream into your own link room."
        />
      </fieldset>

      <div className="space-y-3 border-t border-line pt-4">
        <h3 className="text-sm font-medium">OBS</h3>
        <p className="text-sm text-muted">
          In OBS, open Settings → Stream, choose the <strong>WHIP</strong> service and enter this server URL and your
          stream key as the Bearer token. Use H.264 or AV1 with B-frames off and a 1–2 second keyframe interval.
        </p>
        {key.data && <CopyField label="Server" value={key.data.whipUrl} />}
        {regenerate.data ? (
          <div className="space-y-1">
            <CopyField label="Stream key" value={regenerate.data.key} />
            <p className="text-xs text-muted">Copy it now: it is shown only once.</p>
          </div>
        ) : (
          <p className="text-sm text-muted">
            {key.data?.hasKey
              ? `Stream key created ${new Date(key.data.createdAt! * 1000).toLocaleDateString()}.`
              : 'You have no stream key yet.'}
          </p>
        )}
        <Button variant="secondary" disabled={regenerate.isPending} onClick={() => regenerate.mutate()}>
          <RefreshCw className="size-4" aria-hidden />
          {key.data?.hasKey ? 'Generate a new key (revokes the old one)' : 'Generate stream key'}
        </Button>
        {s.startMode === 'discord_auto' && (
          <fieldset className="space-y-2">
            <legend className="mb-1 text-sm font-medium">When OBS finds no Discord room</legend>
            <Radio
              name="obs-fallback"
              checked={s.obsFallback === 'link'}
              onChange={() => settings.mutate({ ...s, obsFallback: 'link' })}
              label="Stream to my link room"
            />
            <Radio
              name="obs-fallback"
              checked={s.obsFallback === 'refuse'}
              onChange={() => settings.mutate({ ...s, obsFallback: 'refuse' })}
              label="Refuse the stream"
            />
          </fieldset>
        )}
      </div>
    </Section>
  );
}

function Radio(props: { name: string; checked: boolean; onChange: () => void; label: string; hint?: string }) {
  return (
    <label className="flex cursor-pointer items-start gap-2.5 text-sm">
      <input type="radio" className="mt-1 accent-accent" name={props.name} checked={props.checked} onChange={props.onChange} />
      <span>
        <span className="font-medium">{props.label}</span>
        {props.hint && <span className="block text-muted">{props.hint}</span>}
      </span>
    </label>
  );
}

function CopyField({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => setCopied(false), [value]);
  return (
    <div className="flex items-center gap-2">
      <span className="w-20 shrink-0 text-sm text-muted">{label}</span>
      <code className="min-w-0 flex-1 truncate rounded-md border border-line bg-bg px-2 py-1.5 text-xs">{value}</code>
      <Button
        variant="ghost"
        className="px-2"
        aria-label={`Copy ${label}`}
        onClick={() => void navigator.clipboard.writeText(value).then(() => setCopied(true))}
      >
        <Copy className="size-4" />
      </Button>
      {copied && <span className="text-xs text-muted">Copied</span>}
    </div>
  );
}

function RoomSettings({ slug }: { slug: string }) {
  const room = useRoom(slug);
  const update = useUpdateRoom(slug);
  const allowed = useAllowed(slug);
  const setAllowed = useSetAllowed(slug);
  const remove = useRemoveAllowed(slug);
  const [handle, setHandle] = useState('');

  function add(e: FormEvent) {
    e.preventDefault();
    setAllowed.mutate({ handle: handle.trim().toLowerCase(), canStream: false }, { onSuccess: () => setHandle('') });
  }

  return (
    <Section title="Your link room" description="Your own room, shared through its link.">
      <p className="text-sm">
        <Link to={`/r/${slug}`} className="font-semibold text-accent hover:underline">
          {window.location.origin}/r/{slug}
        </Link>
      </p>
      <label className="block max-w-xs space-y-1.5">
        <span className="text-sm font-medium">Who can watch</span>
        <select
          className="h-9 w-full rounded-md border border-line bg-bg px-2 text-sm"
          value={room.data?.visibility ?? 'invite_only'}
          onChange={(e) => update.mutate(e.target.value as Visibility)}
        >
          {(Object.keys(visibilityLabel) as Visibility[]).map((v) => (
            <option key={v} value={v}>
              {visibilityLabel[v]}
            </option>
          ))}
        </select>
      </label>

      <div className="space-y-2 border-t border-line pt-4">
        <h3 className="text-sm font-medium">Allowed people</h3>
        <p className="text-sm text-muted">
          They can always enter, even when the room is invite only. Co-streamers can also stream here.
        </p>
        <form onSubmit={add} className="flex max-w-sm gap-2">
          <Input value={handle} onChange={(e) => setHandle(e.target.value)} placeholder="handle" aria-label="Handle to allow" />
          <Button type="submit" variant="secondary" disabled={!handle.trim() || setAllowed.isPending}>
            Allow
          </Button>
        </form>
        <AllowFriends slug={slug} />
        {(setAllowed.error || remove.error) && (
          <p className="text-sm text-danger">{(setAllowed.error ?? remove.error)?.message}</p>
        )}
        <ul className="divide-y divide-line">
          {allowed.data?.map((a) => (
            <li key={a.user.id} className="flex items-center justify-between gap-3 py-2 text-sm">
              <span>
                <span className="font-semibold">{a.user.displayName}</span> <span className="text-muted">@{a.user.handle}</span>
              </span>
              <span className="flex items-center gap-3">
                <label className="flex items-center gap-1.5 text-muted">
                  <input
                    type="checkbox"
                    className="accent-accent"
                    checked={a.canStream}
                    onChange={(e) => setAllowed.mutate({ handle: a.user.handle, canStream: e.target.checked })}
                  />
                  Co-streamer
                </label>
                <Button variant="ghost" className="h-7 px-2" onClick={() => remove.mutate(a.user.handle)}>
                  Remove
                </Button>
              </span>
            </li>
          ))}
        </ul>
      </div>
      <BannedPeople slug={slug} />
    </Section>
  );
}

function BannedPeople({ slug }: { slug: string }) {
  const bans = useBans(slug);
  const unban = useUnban(slug);
  return (
    <div className="space-y-2 border-t border-line pt-4">
      <h3 className="text-sm font-medium">Banned people</h3>
      <p className="text-sm text-muted">
        They cannot enter, not even with an invite link. Ban someone from the list of people in your room.
      </p>
      {unban.error && <p className="text-sm text-danger">{unban.error.message}</p>}
      {bans.data?.length === 0 && <p className="text-sm text-muted">Nobody is banned.</p>}
      <ul className="divide-y divide-line">
        {bans.data?.map((b) => (
          <li key={b.user.id} className="flex items-center justify-between gap-3 py-2 text-sm">
            <span>
              <span className="font-semibold">{b.user.displayName}</span> <span className="text-muted">@{b.user.handle}</span>
            </span>
            <Button variant="ghost" className="h-7 px-2" disabled={unban.isPending} onClick={() => unban.mutate(b.user.handle)}>
              Unban
            </Button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function DiscordServers() {
  const guilds = useGuilds(true);
  const update = useUpdateGuild();
  return (
    <Section
      title="Discord servers"
      description="Friend suggestions come from CupStream users on these servers. Large servers are off by default."
    >
      {guilds.data?.length === 0 && <p className="text-sm text-muted">No servers yet.</p>}
      <ul className="divide-y divide-line">
        {guilds.data?.map((g) => (
          <li key={g.id} className="flex items-center justify-between gap-3 py-2 text-sm">
            <span className="flex items-center gap-2">
              {g.iconUrl ? (
                <img src={g.iconUrl} alt="" className="size-6 rounded-full" />
              ) : (
                <span className="size-6 rounded-full bg-raised" />
              )}
              <span className="font-medium">{g.name}</span>
              <span className="text-muted">{g.approxMembers.toLocaleString()} members</span>
            </span>
            <label className="flex items-center gap-1.5 text-muted">
              <input
                type="checkbox"
                className="accent-accent"
                checked={g.suggestionsEnabled}
                onChange={(e) => update.mutate({ id: g.id, suggestionsEnabled: e.target.checked })}
              />
              Suggestions
            </label>
          </li>
        ))}
      </ul>
    </Section>
  );
}

function Sessions() {
  const sessions = useSessions();
  const revoke = useRevokeSession();
  return (
    <Section title="Sessions" description="Browsers and apps signed in to your account.">
      <ul className="divide-y divide-line">
        {sessions.data?.map((s) => (
          <li key={s.id} className="flex items-center justify-between gap-3 py-2 text-sm">
            <span className="flex items-center gap-2">
              <Monitor className="size-4 text-muted" aria-hidden />
              <span>
                {s.kind === 'app' ? s.deviceName || 'CupStream app' : 'Browser'}
                {s.current && <span className="text-accent"> · this browser</span>}
              </span>
              <span className="text-muted">last used {new Date(s.lastSeenAt * 1000).toLocaleString()}</span>
            </span>
            {!s.current && (
              <Button variant="ghost" className="h-7 px-2" disabled={revoke.isPending} onClick={() => revoke.mutate(s.id)}>
                Sign out
              </Button>
            )}
          </li>
        ))}
      </ul>
    </Section>
  );
}
