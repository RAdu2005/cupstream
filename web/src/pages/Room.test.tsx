import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { vi } from 'vitest';
import type { Room, Stream, Subscribed } from '@/api/types.gen';
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
  visibility: 'invite_only',
  permissions: { watch: true, stream: false, chat: true, moderateStreams: false, moderateChat: false },
  streams: [],
};
const stream: Stream = {
  id: 's1',
  user: alice,
  source: 'obs',
  state: 'starting',
  startedAt: 1_800_000_000,
  viewers: 0,
  bitrate: 0,
  lossRatio: 0,
};

function setup(roomResponses: Response[]) {
  const fetchMock = vi.fn(async (url: string) => {
    if (url === '/api/me') return json(200, { user: bob });
    if (url === '/api/rooms/abc') return roomResponses.shift() ?? json(200, room);
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

async function connected() {
  await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
  const ws = FakeWebSocket.last;
  act(() => ws.open());
  expect(ws.sent).toEqual([{ type: 'subscribe', room: 'abc' }]);
  return ws;
}

test('the room follows streams, presence and chat over the WebSocket', async () => {
  const fetchMock = setup([json(200, room)]);
  expect(await screen.findByText('Nobody is live in this room')).toBeInTheDocument();
  const ws = await connected();

  act(() =>
    ws.receive({
      type: 'subscribed',
      room: 'abc',
      streams: [],
      presence: { members: [alice, bob], anonymous: 0 },
      chat: [{ id: 'm1', user: alice, body: 'welcome', createdAt: Date.now() / 1000 }],
    }),
  );
  const present = within(screen.getByRole('region', { name: 'In this room' }));
  expect(present.getByText('Alice')).toBeInTheDocument();
  expect(present.getByText('Bob')).toBeInTheDocument();
  const chat = screen.getByRole('region', { name: 'Chat' });
  expect(chat).toHaveTextContent('Alice: welcome');

  // A stream starts and goes live without the page polling.
  act(() => ws.receive({ type: 'stream', room: 'abc', stream }));
  expect(screen.getByText('Starting…')).toBeInTheDocument();
  expect(screen.getByText('LIVE')).toBeInTheDocument(); // Alice's presence badge
  act(() => ws.receive({ type: 'stream', room: 'abc', stream: { ...stream, state: 'live', viewers: 3 } }));
  expect(screen.getByTitle('Viewers')).toHaveTextContent('3');
  act(() => ws.receive({ type: 'streamEnded', room: 'abc', streamId: 's1' }));
  expect(screen.getByText('Nobody is live in this room')).toBeInTheDocument();

  // Others join; chat arrives and can be sent.
  act(() => ws.receive({ type: 'presence', room: 'abc', presence: { members: [alice, bob], anonymous: 2 } }));
  expect(screen.getByText('2 guests without an account')).toBeInTheDocument();
  fireEvent.change(screen.getByRole('textbox', { name: 'Message' }), { target: { value: 'hi' } });
  fireEvent.click(screen.getByRole('button', { name: 'Send' }));
  expect(ws.sent[1]).toMatchObject({ type: 'chat', room: 'abc', body: 'hi' });
  act(() =>
    ws.receive({
      type: 'chat',
      room: 'abc',
      message: { id: 'm2', user: bob, body: 'hi', createdAt: Date.now() / 1000 },
    }),
  );
  expect(chat).toHaveTextContent('Bob: hi');
  act(() => ws.receive({ type: 'chatDeleted', room: 'abc', id: 'm1' }));
  expect(chat).not.toHaveTextContent('welcome');

  // The room was fetched once: no polling.
  expect(fetchMock.mock.calls.filter(([url]) => url === '/api/rooms/abc')).toHaveLength(1);
});

test('fetches the room again after reconnecting', async () => {
  const fetchMock = setup([json(200, room), json(200, { ...room, visibility: 'everyone' })]);
  await screen.findByText('Nobody is live in this room');
  const ws = await connected();
  const snapshot: Subscribed = {
    type: 'subscribed',
    room: 'abc',
    streams: [],
    presence: { members: [bob], anonymous: 0 },
    chat: [],
  };
  act(() => ws.receive(snapshot));

  vi.useFakeTimers();
  act(() => ws.drop());
  expect(screen.getByRole('status')).toHaveTextContent('Reconnecting…');
  act(() => vi.advanceTimersByTime(2000));
  vi.useRealTimers();
  const again = FakeWebSocket.last;
  expect(again).not.toBe(ws);
  act(() => again.open());
  act(() => again.receive({ ...snapshot, streams: [{ ...stream, state: 'live' }] }));
  expect(await screen.findByText('Everyone with the link')).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([url]) => url === '/api/rooms/abc')).toHaveLength(2);
});

test('shows the refusal when access ends', async () => {
  setup([json(200, room), json(403, { error: 'you cannot enter this room', code: 'not_allowed' })]);
  await screen.findByText('Nobody is live in this room');
  const ws = await connected();
  act(() =>
    ws.receive({ type: 'refused', room: 'abc', code: 'not_allowed', error: 'you can no longer enter this room' }),
  );
  expect(await screen.findByText('This room is invite only. Ask its owner to let you in.')).toBeInTheDocument();
});
