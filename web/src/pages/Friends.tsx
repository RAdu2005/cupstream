import { Search } from 'lucide-react';
import { useState, type FormEvent, type ReactNode } from 'react';
import { Link, Navigate } from 'react-router';
import { useAccount } from '@/api/account';
import { useMe } from '@/api/auth';
import { ApiError } from '@/api/client';
import {
  useAcceptRequest,
  useBlock,
  useBlocked,
  useDismissSuggestion,
  useFriends,
  useFriendsLive,
  useLookup,
  useRemoveRequest,
  useSendRequest,
  useSuggestions,
  useUnblock,
  useUnfriend,
} from '@/api/friends';
import type { LiveFriend, User, UserRelation } from '@/api/types.gen';
import { AllowInRoomButton } from '@/components/AllowFriends';
import { Button, Card, Input } from '@/components/ui';

export function Friends() {
  const me = useMe();
  if (me.isPending) return null;
  if (!me.data) return <Navigate to="/login?next=/friends" replace />;

  return (
    <div className="mx-auto w-full max-w-3xl space-y-6 p-4 sm:p-8">
      <h1 className="text-2xl font-bold">Friends</h1>
      <AddFriend />
      <Requests />
      <FriendList />
      <Suggestions />
      <Blocked />
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

function Avatar({ user }: { user: User }) {
  return user.avatarUrl ? (
    <img src={user.avatarUrl} alt="" className="size-8 shrink-0 rounded-full" />
  ) : (
    <span aria-hidden className="flex size-8 shrink-0 items-center justify-center rounded-full bg-raised font-semibold">
      {user.displayName.slice(0, 1).toUpperCase()}
    </span>
  );
}

/** A person with actions on the right. */
function Row({ user, detail, children }: { user: User; detail?: ReactNode; children?: ReactNode }) {
  return (
    <li className="flex flex-wrap items-center justify-between gap-3 py-2.5 text-sm first:pt-0 last:pb-0">
      <span className="flex min-w-0 items-center gap-3">
        <Avatar user={user} />
        <span className="min-w-0">
          <span className="block truncate">
            <span className="font-semibold">{user.displayName}</span> <span className="text-muted">@{user.handle}</span>
          </span>
          {detail && <span className="block truncate text-xs text-muted">{detail}</span>}
        </span>
      </span>
      <span className="flex flex-wrap items-center gap-2">{children}</span>
    </li>
  );
}

function Failure({ error }: { error: Error | null }) {
  return error ? <p className="text-sm text-danger">{error.message}</p> : null;
}

function AddFriend() {
  const [input, setInput] = useState('');
  const [handle, setHandle] = useState('');
  const lookup = useLookup(handle);

  function submit(e: FormEvent) {
    e.preventDefault();
    setHandle(input.trim().replace(/^@/, '').toLowerCase());
  }

  return (
    <Section title="Add a friend" description="Enter their exact CupStream handle. They see your request and can accept it.">
      <form onSubmit={submit} className="flex max-w-sm gap-2">
        <Input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="handle"
          aria-label="Handle to look up"
        />
        <Button type="submit" variant="secondary" disabled={!input.trim()}>
          <Search className="size-4" aria-hidden /> Find
        </Button>
      </form>
      {handle !== '' && lookup.data === null && <p className="text-sm text-muted">Nobody has the handle @{handle}.</p>}
      {lookup.error && (
        <p className="text-sm text-muted">
          {lookup.error instanceof ApiError && lookup.error.status === 400 ? 'That is you.' : lookup.error.message}
        </p>
      )}
      {lookup.data && (
        <ul>
          <Row user={lookup.data.user}>
            <RelationActions relation={lookup.data} />
          </Row>
        </ul>
      )}
    </Section>
  );
}

/** What the user can do about someone, given how they stand. */
function RelationActions({ relation }: { relation: UserRelation }) {
  const send = useSendRequest();
  const accept = useAcceptRequest();
  const remove = useRemoveRequest();
  const unblock = useUnblock();
  const id = relation.user.id;
  const error = send.error ?? accept.error ?? remove.error ?? unblock.error;

  return (
    <>
      {relation.relation === 'none' && (
        <Button disabled={send.isPending} onClick={() => send.mutate(id)}>
          Send request
        </Button>
      )}
      {relation.relation === 'outgoing' && (
        <>
          <span className="text-muted">Request sent</span>
          <Button variant="ghost" disabled={remove.isPending} onClick={() => remove.mutate(id)}>
            Cancel
          </Button>
        </>
      )}
      {relation.relation === 'incoming' && (
        <Button disabled={accept.isPending} onClick={() => accept.mutate(id)}>
          Accept request
        </Button>
      )}
      {relation.relation === 'friends' && <span className="text-muted">Friends</span>}
      {relation.relation === 'blocked' && (
        <Button variant="secondary" disabled={unblock.isPending} onClick={() => unblock.mutate(id)}>
          Unblock
        </Button>
      )}
      {error && <span className="w-full text-danger">{error.message}</span>}
    </>
  );
}

function Requests() {
  const friends = useFriends();
  const accept = useAcceptRequest();
  const remove = useRemoveRequest();
  const block = useBlock();
  const incoming = friends.data?.incoming ?? [];
  const outgoing = friends.data?.outgoing ?? [];
  if (incoming.length + outgoing.length === 0) return null;

  return (
    <Section title="Requests">
      {incoming.length > 0 && (
        <ul className="divide-y divide-line" aria-label="Requests to you">
          {incoming.map(({ user, createdAt }) => (
            <Row key={user.id} user={user} detail={`Asked ${new Date(createdAt * 1000).toLocaleDateString()}`}>
              <Button disabled={accept.isPending} onClick={() => accept.mutate(user.id)}>
                Accept
              </Button>
              <Button variant="secondary" disabled={remove.isPending} onClick={() => remove.mutate(user.id)}>
                Decline
              </Button>
              <Button
                variant="ghost"
                disabled={block.isPending}
                onClick={() => confirm(`Block ${user.displayName}? They will not be able to find you or ask again.`) && block.mutate(user.id)}
              >
                Block
              </Button>
            </Row>
          ))}
        </ul>
      )}
      {outgoing.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-sm font-medium">Sent</h3>
          <ul className="divide-y divide-line" aria-label="Requests you sent">
            {outgoing.map(({ user }) => (
              <Row key={user.id} user={user} detail="Waiting for an answer">
                <Button variant="ghost" disabled={remove.isPending} onClick={() => remove.mutate(user.id)}>
                  Cancel
                </Button>
              </Row>
            ))}
          </ul>
        </div>
      )}
      <Failure error={accept.error ?? remove.error ?? block.error} />
    </Section>
  );
}

function FriendList() {
  const friends = useFriends();
  const live = useFriendsLive();
  const unfriend = useUnfriend();
  const block = useBlock();
  const liveBy = new Map<string, LiveFriend>((live.data ?? []).map((l) => [l.stream.user.id, l]));
  const list = friends.data?.friends ?? [];

  return (
    <Section
      title="Your friends"
      description="Friends see when you go live in a room they may enter. Allow them into your room in one click."
    >
      {friends.data && list.length === 0 && <p className="text-sm text-muted">No friends yet. Add someone above.</p>}
      <ul className="divide-y divide-line" aria-label="Friends">
        {list.map(({ user, since }) => {
          const l = liveBy.get(user.id);
          return (
            <Row
              key={user.id}
              user={user}
              detail={
                l ? (
                  <span className="flex items-center gap-1.5">
                    <span className="rounded bg-live px-1 text-[10px] font-bold text-white">
                      {l.stream.state === 'live' ? 'LIVE' : l.stream.state === 'starting' ? 'STARTING' : 'RECONNECTING'}
                    </span>
                    in {l.room.owner ? (l.room.owner.id === user.id ? 'their room' : `${l.room.owner.displayName}'s room`) : 'a Discord room'}
                  </span>
                ) : (
                  `Friends since ${new Date(since * 1000).toLocaleDateString()}`
                )
              }
            >
              {l && (
                <Link to={`/r/${l.room.slug}`}>
                  <Button className="h-7 px-2 text-xs">Watch</Button>
                </Link>
              )}
              <AllowInRoomButton user={user} />
              <Button
                variant="ghost"
                className="h-7 px-2 text-xs"
                disabled={unfriend.isPending}
                onClick={() => confirm(`Remove ${user.displayName} from your friends?`) && unfriend.mutate(user.id)}
              >
                Unfriend
              </Button>
              <Button
                variant="ghost"
                className="h-7 px-2 text-xs"
                disabled={block.isPending}
                onClick={() => confirm(`Block ${user.displayName}? This also removes them from your friends.`) && block.mutate(user.id)}
              >
                Block
              </Button>
            </Row>
          );
        })}
      </ul>
      <Failure error={unfriend.error ?? block.error} />
    </Section>
  );
}

function Suggestions() {
  const account = useAccount();
  const hasDiscord = account.data?.identities.some((i) => i.provider === 'discord') ?? false;
  const suggestions = useSuggestions(hasDiscord);
  const send = useSendRequest();
  const dismiss = useDismissSuggestion();
  if (!account.data) return null;

  return (
    <Section
      title="People you may know"
      description="CupStream users on your Discord servers. Choose which servers suggest people in Settings."
    >
      {!hasDiscord && (
        <p className="text-sm text-muted">
          <Link to="/settings" className="font-semibold text-accent hover:underline">
            Link Discord
          </Link>{' '}
          to see people from your servers.
        </p>
      )}
      {suggestions.data?.length === 0 && <p className="text-sm text-muted">No suggestions right now.</p>}
      <ul className="divide-y divide-line" aria-label="Suggestions">
        {suggestions.data?.map(({ user, guilds }) => (
          <Row key={user.id} user={user} detail={`On ${guilds.map((g) => g.name).join(', ')}`}>
            <Button className="h-7 px-2 text-xs" disabled={send.isPending} onClick={() => send.mutate(user.id)}>
              Add friend
            </Button>
            <Button variant="ghost" className="h-7 px-2 text-xs" disabled={dismiss.isPending} onClick={() => dismiss.mutate(user.id)}>
              Hide
            </Button>
          </Row>
        ))}
      </ul>
      <Failure error={send.error ?? dismiss.error} />
    </Section>
  );
}

function Blocked() {
  const blocked = useBlocked();
  const unblock = useUnblock();
  if (!blocked.data?.length) return null;

  return (
    <Section title="Blocked" description="They cannot find you by your handle or send you friend requests.">
      <ul className="divide-y divide-line">
        {blocked.data.map((user) => (
          <Row key={user.id} user={user}>
            <Button variant="ghost" className="h-7 px-2 text-xs" disabled={unblock.isPending} onClick={() => unblock.mutate(user.id)}>
              Unblock
            </Button>
          </Row>
        ))}
      </ul>
      <Failure error={unblock.error} />
    </Section>
  );
}
