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

/** The download test's answer: a few chunks of data. */
function testDownload() {
  let chunks = 4;
  return new Response(
    new ReadableStream({ pull: (c) => (chunks-- > 0 ? c.enqueue(new Uint8Array(16 << 10)) : c.close()) }),
  );
}

function setup(roomResponses: Response[]) {
  const fetchMock = vi.fn(async (url: string) => {
    if (url === '/api/me') return json(200, { user: bob });
    if (url.startsWith('/api/bandwidth-test')) return testDownload();
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

test('cards start streams in a focus or grid layout, up to four at once', async () => {
  const streams = ['s1', 's2', 's3', 's4', 's5'].map((id, i) => ({
    ...stream,
    id,
    state: 'live' as const,
    startedAt: stream.startedAt + i,
    user: { id: `u${i + 10}`, handle: `user${i}`, displayName: `User ${i}` },
    thumbnailAt: i === 4 ? 1_800_000_000_123 : undefined,
  }));
  setup([json(200, { ...room, streams })]);
  const cards = within(await screen.findByRole('region', { name: 'Live in this room' }));
  expect(cards.getByText('Choose a stream to watch')).toBeInTheDocument();

  // Cards show the sender's latest thumbnail, refreshed as it changes.
  expect(cards.getByRole('button', { name: "Watch User 4's stream" }).querySelector('img')).toHaveAttribute(
    'src',
    '/api/streams/s5/thumbnail?v=1800000000123',
  );
  expect(cards.getByRole('button', { name: "Watch User 0's stream" }).querySelector('img')).toBeNull();
  fireEvent.click(cards.getByRole('button', { name: "Watch User 0's stream" }));
  const main = screen.getByRole('group', { name: "User 0's stream" });
  expect(within(main).getByRole('button', { name: 'Mute' })).toBeInTheDocument(); // the focused stream is heard
  expect(screen.queryByRole('radiogroup', { name: 'Layout' })).not.toBeInTheDocument();

  // Opening a second stream checks the connection first (the test fails
  // here, which does not stop the viewer).
  fireEvent.click(cards.getByRole('button', { name: "Watch User 1's stream" }));
  await screen.findByRole('group', { name: "User 1's stream" });
  // User 1 has the focus now; User 0 became a small, muted background tile.
  const tile0 = within(screen.getByRole('group', { name: "User 0's stream" }));
  expect(tile0.getByRole('button', { name: 'Unmute' })).toBeInTheDocument();
  expect(tile0.queryByRole('button', { name: 'Fullscreen' })).not.toBeInTheDocument();
  expect(screen.getByText('Watching 2 of 4')).toBeInTheDocument();

  const video0 = screen.getByRole('group', { name: "User 0's stream" }).querySelector('video');
  fireEvent.click(screen.getByRole('radio', { name: 'Grid' }));
  expect(within(screen.getByRole('group', { name: "User 0's stream" })).getByRole('button', { name: 'Fullscreen' }))
    .toBeInTheDocument();
  // Changing the layout keeps the video elements, so playback does not restart.
  expect(screen.getByRole('group', { name: "User 0's stream" }).querySelector('video')).toBe(video0);

  fireEvent.click(cards.getByRole('button', { name: "Watch User 2's stream" }));
  await screen.findByRole('group', { name: "User 2's stream" });
  fireEvent.click(cards.getByRole('button', { name: "Watch User 3's stream" }));
  expect(await screen.findByText(/You can watch up to 4 streams at once/)).toBeInTheDocument();
  expect(cards.getByRole('button', { name: "Watch User 4's stream" })).toBeDisabled();

  // Pausing one makes room for another.
  fireEvent.click(within(screen.getByRole('group', { name: "User 0's stream" })).getByRole('button', { name: 'Pause' }));
  expect(cards.getByRole('button', { name: "Watch User 4's stream" })).toBeEnabled();
  fireEvent.click(within(screen.getByRole('group', { name: "User 1's stream" })).getByRole('button', { name: 'Stop watching' }));
  expect(screen.queryByRole('group', { name: "User 1's stream" })).not.toBeInTheDocument();
  expect(cards.getByRole('button', { name: "Watch User 1's stream" })).toBeInTheDocument();
});

test('warns before opening a stream the connection may not carry', async () => {
  const streams = ['s1', 's2'].map((id, i) => ({
    ...stream,
    id,
    state: 'live' as const,
    bitrate: 1e12, // more than any connection
    user: { id: `u${i + 10}`, handle: `user${i}`, displayName: `User ${i}` },
  }));
  const fetchMock = setup([json(200, { ...room, streams })]);
  const cards = within(await screen.findByRole('region', { name: 'Live in this room' }));
  fireEvent.click(cards.getByRole('button', { name: "Watch User 0's stream" }));
  expect(fetchMock.mock.calls.some(([url]) => url.startsWith('/api/bandwidth-test'))).toBe(false); // one stream: no test

  fireEvent.click(cards.getByRole('button', { name: "Watch User 1's stream" }));
  const dialog = within(await screen.findByRole('dialog', { name: 'Your connection may not keep up' }));
  expect(dialog.getByText(/Watching 2 streams needs about 2000000 Mbps/)).toBeInTheDocument();
  expect(fetchMock.mock.calls.filter(([url]) => url.startsWith('/api/bandwidth-test'))).toHaveLength(1);

  fireEvent.click(dialog.getByRole('button', { name: 'Pause the others' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  const first = within(screen.getByRole('group', { name: "User 0's stream" }));
  expect(first.getByRole('button', { name: 'Resume' })).toBeInTheDocument();
  expect(within(screen.getByRole('group', { name: "User 1's stream" })).getByRole('button', { name: 'Pause' }))
    .toBeInTheDocument();

  // The capacity is measured once per tab.
  fireEvent.click(first.getByRole('button', { name: 'Stop watching' }));
  const again = within(screen.getByRole('region', { name: 'Live in this room' }));
  fireEvent.click(again.getByRole('button', { name: "Watch User 0's stream" }));
  await screen.findByRole('dialog', { name: 'Your connection may not keep up' });
  expect(fetchMock.mock.calls.filter(([url]) => url.startsWith('/api/bandwidth-test'))).toHaveLength(1);
});
