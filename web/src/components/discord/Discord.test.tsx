import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { vi } from 'vitest';
import type { Room } from '@/api/types.gen';
import { routes } from '@/routes';
import { FakeWebSocket } from '@/test/fakeWebSocket';

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const bob = { id: 'u2', handle: 'bob', displayName: 'Bob' };
const discordRoom: Room = {
  id: 'r1',
  slug: 'abc',
  kind: 'discord',
  permissions: { watch: true, stream: true, chat: true, moderateStreams: false, moderateChat: false },
  streams: [],
  discord: { guildName: 'Friends', channelName: 'General', url: 'https://discord.com/channels/g/c' },
};

function setup(roomResponses: Response[], channel = { guildName: 'Friends', channelName: 'General', url: 'https://discord.com/channels/g/c' }) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === '/api/me') return json(200, { user: bob });
      if (url === '/api/rooms/abc') return roomResponses.shift() ?? json(200, discordRoom);
      if (url === '/api/rooms/abc/discord') return json(200, channel);
      throw new Error(`unexpected fetch ${url}`);
    }),
  );
  vi.stubGlobal('WebSocket', FakeWebSocket);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={createMemoryRouter(routes, { initialEntries: ['/r/abc'] })} />
    </QueryClientProvider>,
  );
}

const notInVoice = () => json(403, { error: 'you cannot enter this room', code: 'join_voice_channel' });

afterEach(() => {
  vi.useRealTimers();
  FakeWebSocket.instances = [];
});

test('someone not in the voice channel is asked to join it, and enters once they do', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  setup([notInVoice(), notInVoice()]);
  expect(await screen.findByText('Join the voice channel to watch')).toBeInTheDocument();
  expect(await screen.findByText('#General')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: /Open in Discord/ })).toHaveAttribute('href', 'https://discord.com/channels/g/c');

  // Tried again every few seconds; the third answer lets them in.
  await act(() => vi.advanceTimersByTimeAsync(6500));
  expect(await screen.findByRole('heading', { name: '#General' })).toBeInTheDocument();
  expect(screen.getByText(/Friends/)).toBeInTheDocument();
});

test('names are not shown to people outside the server', async () => {
  setup([notInVoice()], { guildName: '', channelName: '', url: 'https://discord.com/channels/g/c' });
  expect(await screen.findByText(/Only people in the voice channel can watch/)).toBeInTheDocument();
  expect(screen.queryByText(/General/)).not.toBeInTheDocument();
});

test('the chat of a Discord room is collapsed until opened', async () => {
  setup([json(200, discordRoom)]);
  const toggle = await screen.findByRole('button', { name: /Chat/ });
  expect(toggle).toHaveAttribute('aria-expanded', 'false');
  expect(screen.queryByRole('region', { name: 'Chat' })).not.toBeInTheDocument();
  fireEvent.click(toggle);
  expect(screen.getByRole('region', { name: 'Chat' })).toBeInTheDocument();
});

test('a streamer who left voice sees a countdown', async () => {
  setup([json(200, discordRoom)]);
  await screen.findByRole('heading', { name: '#General' });
  await waitFor(() => expect(FakeWebSocket.instances.length).toBeGreaterThan(0));
  const ws = FakeWebSocket.last;
  act(() => ws.open());
  act(() => ws.receive({ type: 'streamGrace', roomSlug: 'abc', streamId: 's1', endsInMs: 12_000 }));
  expect(screen.getByRole('alert')).toHaveTextContent('your stream ends in 12 s');
  act(() => ws.receive({ type: 'streamGrace', roomSlug: 'abc', streamId: 's1', endsInMs: 0 }));
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});
