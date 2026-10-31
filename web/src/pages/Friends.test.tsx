import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { vi } from 'vitest';
import type { Friends, LiveFriend } from '@/api/types.gen';
import { routes } from '@/routes';
import { FakeWebSocket } from '@/test/fakeWebSocket';

function json(status: number, body?: unknown) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const alice = { id: 'u1', handle: 'alice', displayName: 'Alice' };
const bob = { id: 'u2', handle: 'bob', displayName: 'Bob' };
const carol = { id: 'u3', handle: 'carol', displayName: 'Carol' };
const dan = { id: 'u4', handle: 'dan', displayName: 'Dan' };
const eve = { id: 'u5', handle: 'eve', displayName: 'Eve' };

const friends: Friends = {
  friends: [{ user: bob, since: 1_800_000_000 }],
  incoming: [{ user: carol, createdAt: 1_800_000_000 }],
  outgoing: [],
};
const live: LiveFriend[] = [
  {
    stream: { id: 's1', user: bob, source: 'obs', state: 'live', startedAt: 1, viewers: 0, bitrate: 0, lossRatio: 0 },
    room: { slug: 'bbb', kind: 'link', owner: bob },
  },
];

function setup() {
  const calls: string[] = [];
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    calls.push(`${method} ${url}${init?.body ? ' ' + String(init.body) : ''}`);
    switch (`${method} ${url}`) {
      case 'GET /api/me':
        return json(200, { user: alice });
      case 'GET /api/account':
        return json(200, {
          user: alice,
          identities: [{ provider: 'discord', username: 'alice' }],
          settings: { startMode: 'link', obsFallback: 'link' },
          roomSlug: 'abc',
        });
      case 'GET /api/friends':
        return json(200, friends);
      case 'GET /api/friends/live':
        return json(200, live);
      case 'GET /api/friends/suggestions':
        return json(200, [{ user: dan, guilds: [{ id: 'g1', name: 'Cup' }] }]);
      case 'GET /api/friends/blocked':
        return json(200, []);
      case 'GET /api/rooms/abc/allowed':
        return json(200, []);
      case 'GET /api/friends/lookup?handle=eve':
        return json(200, { user: eve, relation: 'none' });
      case 'GET /api/friends/lookup?handle=zed':
        return json(404, { error: 'no user has this handle' });
      case 'PUT /api/rooms/abc/allowed/bob':
        return json(200, { user: bob, canStream: false });
      case 'POST /api/friends/requests/u3/accept':
        return json(200, { user: carol, relation: 'friends' });
      case 'POST /api/friends/requests':
        return json(200, { user: dan, relation: 'outgoing' });
    }
    throw new Error(`unexpected fetch ${method} ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('WebSocket', FakeWebSocket);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={createMemoryRouter(routes, { initialEntries: ['/friends'] })} />
    </QueryClientProvider>,
  );
  return calls;
}

test('the friends page lists friends, requests and suggestions, with their actions', async () => {
  const calls = setup();

  // Bob is live in his room, and can be allowed into Alice's in one click.
  const list = await screen.findByRole('list', { name: 'Friends' });
  const bobRow = await within(list).findByText('LIVE');
  expect(bobRow.parentElement).toHaveTextContent('in their room');
  expect(within(list).getByRole('link')).toHaveAttribute('href', '/r/bbb');
  fireEvent.click(await within(list).findByRole('button', { name: /Allow in your room/ }));
  await waitFor(() => expect(calls).toContain('PUT /api/rooms/abc/allowed/bob {"canStream":false}'));

  // Carol's request, also counted in the navigation.
  expect(screen.getByRole('link', { name: 'Friends, 1 new request' })).toBeInTheDocument();
  const incoming = screen.getByRole('list', { name: 'Requests to you' });
  fireEvent.click(within(incoming).getByRole('button', { name: 'Accept' }));
  await waitFor(() => expect(calls).toContain('POST /api/friends/requests/u3/accept'));

  // A suggestion from a shared server.
  const suggestions = await screen.findByRole('list', { name: 'Suggestions' });
  expect(within(suggestions).getByText('On Cup')).toBeInTheDocument();
  fireEvent.click(within(suggestions).getByRole('button', { name: 'Add friend' }));
  await waitFor(() => expect(calls).toContain('POST /api/friends/requests {"userId":"u4"}'));

  // Looking someone up by exact handle.
  const input = screen.getByLabelText('Handle to look up');
  fireEvent.change(input, { target: { value: 'zed' } });
  fireEvent.click(screen.getByRole('button', { name: 'Find' }));
  expect(await screen.findByText('Nobody has the handle @zed.')).toBeInTheDocument();
  fireEvent.change(input, { target: { value: '@Eve' } });
  fireEvent.click(screen.getByRole('button', { name: 'Find' }));
  expect(await screen.findByRole('button', { name: 'Send request' })).toBeInTheDocument();
});

test('notifications appear anywhere and refresh the friends lists', async () => {
  const calls = setup();
  await screen.findByRole('list', { name: 'Friends' });
  await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
  const ws = FakeWebSocket.last;
  act(() => ws.open());
  const before = calls.filter((c) => c === 'GET /api/friends').length;

  act(() => ws.receive({ type: 'notification', kind: 'friendLive', user: bob, roomSlug: 'bbb' }));
  const toast = await screen.findByRole('status');
  expect(toast).toHaveTextContent('Bob is live.');
  expect(within(toast).getByRole('link', { name: 'Watch' })).toHaveAttribute('href', '/r/bbb');
  await waitFor(() => expect(calls.filter((c) => c === 'GET /api/friends').length).toBe(before + 1));

  act(() => ws.receive({ type: 'friendsChanged' }));
  await waitFor(() => expect(calls.filter((c) => c === 'GET /api/friends').length).toBe(before + 2));
  fireEvent.click(within(toast).getByRole('button', { name: 'Dismiss' }));
  expect(screen.queryByRole('status')).not.toBeInTheDocument();
});
