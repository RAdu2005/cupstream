import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { vi } from 'vitest';
import type { Room, User } from '@/api/types.gen';
import { routes } from '@/routes';
import { FakeWebSocket } from '@/test/fakeWebSocket';

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const alice = { id: 'u1', handle: 'alice', displayName: 'Alice' };
const bob = { id: 'u2', handle: 'bob', displayName: 'Bob' };
const room: Room = {
  id: 'r1',
  slug: 'abc',
  kind: 'link',
  owner: alice,
  visibility: 'signed_in',
  permissions: { watch: true, stream: true, chat: true, moderateStreams: true, moderateChat: true },
  streams: [],
};

function setup(me: User) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/me') return json(200, { user: me });
    if (url === '/api/rooms/abc') return json(200, me.id === alice.id ? room : { ...room, permissions: { ...room.permissions, moderateStreams: false } });
    if (init?.method === 'POST' || init?.method === 'PUT') return new Response(null, { status: url.includes('/allowed/') ? 200 : 204 });
    if (url === '/api/rooms/abc/allowed' || url === '/api/rooms/abc/bans') return json(200, []);
    throw new Error(`unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('WebSocket', FakeWebSocket);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={createMemoryRouter(routes, { initialEntries: ['/r/abc'] })} />
    </QueryClientProvider>,
  );
  return fetchMock;
}

async function subscribed() {
  await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
  const ws = FakeWebSocket.last;
  act(() => ws.open());
  act(() =>
    ws.receive({ type: 'subscribed', room: 'abc', streams: [], presence: { members: [alice, bob], anonymous: 0 }, chat: [] }),
  );
  return ws;
}

test('the owner kicks, bans and allows people from the presence list', async () => {
  const fetchMock = setup(alice);
  await screen.findByText('Nobody is live in this room');
  await subscribed();
  const present = within(screen.getByRole('region', { name: 'In this room' }));
  expect(present.queryByRole('button', { name: 'Actions for Alice' })).not.toBeInTheDocument();

  const calls = () => fetchMock.mock.calls.map(([url, init]) => `${init?.method ?? 'GET'} ${url}`);
  fireEvent.click(present.getByRole('button', { name: 'Actions for Bob' }));
  fireEvent.click(screen.getByRole('menuitem', { name: 'Kick' }));
  await waitFor(() => expect(calls()).toContain('POST /api/rooms/abc/kick/bob'));

  fireEvent.click(present.getByRole('button', { name: 'Actions for Bob' }));
  fireEvent.click(screen.getByRole('menuitem', { name: 'Add to allowed list' }));
  await waitFor(() => expect(calls()).toContain('PUT /api/rooms/abc/allowed/bob'));

  vi.stubGlobal('confirm', vi.fn(() => false));
  fireEvent.click(present.getByRole('button', { name: 'Actions for Bob' }));
  fireEvent.click(screen.getByRole('menuitem', { name: 'Ban' }));
  expect(calls()).not.toContain('PUT /api/rooms/abc/bans/bob');
  vi.stubGlobal('confirm', vi.fn(() => true));
  fireEvent.click(present.getByRole('button', { name: 'Actions for Bob' }));
  fireEvent.click(screen.getByRole('menuitem', { name: 'Ban' }));
  await waitFor(() => expect(calls()).toContain('PUT /api/rooms/abc/bans/bob'));
});

test('a kicked viewer is told so', async () => {
  setup(bob);
  await screen.findByText('Nobody is live in this room');
  const ws = await subscribed();
  expect(screen.queryByRole('button', { name: 'Actions for Alice' })).not.toBeInTheDocument();
  act(() => ws.receive({ type: 'refused', room: 'abc', code: 'kicked', error: "the room's owner removed you" }));
  expect(await screen.findByText("The room's owner removed you from this room.")).toBeInTheDocument();
});
