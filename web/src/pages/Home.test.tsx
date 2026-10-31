import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { vi } from 'vitest';
import type { Home, Stream } from '@/api/types.gen';
import { routes } from '@/routes';
import { FakeWebSocket } from '@/test/fakeWebSocket';

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const user = (handle: string, name: string) => ({ id: `u-${handle}`, handle, displayName: name });
const alice = user('alice', 'Alice');
const live = (u: ReturnType<typeof user>, viewers: number): Stream => ({
  id: `s-${u.handle}`,
  user: u,
  source: 'web',
  state: 'live',
  startedAt: 1,
  viewers,
  bitrate: 0,
  lossRatio: 0,
});

function setup(home: Home) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === '/api/me') return json(200, { user: alice });
      if (url === '/api/home') return json(200, home);
      return json(404, { error: 'not found' });
    }),
  );
  vi.stubGlobal('WebSocket', FakeWebSocket);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={createMemoryRouter(routes, { initialEntries: ['/'] })} />
    </QueryClientProvider>,
  );
}

test('lists the own room, friends who are live and the rooms one may enter', async () => {
  const bob = user('bob', 'Bob');
  const carol = user('carol', 'Carol');
  const erin = user('erin', 'Erin');
  setup({
    rooms: [
      { slug: 'own', kind: 'link', owner: alice, access: 'own', canStream: true, streams: [] },
      { slug: 'carol', kind: 'link', owner: carol, access: 'pass', canStream: false, streams: [live(carol, 3)] },
      { slug: 'bob', kind: 'link', owner: bob, access: 'allowed', canStream: true, streams: [] },
    ],
    friendsLive: [{ slug: 'erin', kind: 'link', owner: erin, canStream: false, streams: [live(erin, 1)] }],
  });
  expect(await screen.findByRole('link', { name: 'Open room' })).toHaveAttribute('href', '/r/own');

  const friends = within(screen.getByRole('region', { name: 'Friends live now' }));
  expect(friends.getByRole('link')).toHaveAttribute('href', '/r/erin');
  expect(friends.getByText('Live: Erin')).toBeInTheDocument();

  const rooms = within(screen.getByRole('region', { name: 'Rooms you can enter' }));
  const links = rooms.getAllByRole('link');
  expect(links.map((l) => l.getAttribute('href'))).toEqual(['/r/carol', '/r/bob']);
  expect(links[0]).toHaveTextContent('Live: Carol · Invite link');
  expect(within(links[0]!).getByTitle('Viewers')).toHaveTextContent('3');
  expect(links[1]).toHaveTextContent('Nobody is live · Allowed · you can stream here');
});

test('explains where rooms come from when there are none', async () => {
  setup({ rooms: [{ slug: 'own', kind: 'link', owner: alice, access: 'own', canStream: true, streams: [live(alice, 0)] }], friendsLive: [] });
  expect(await screen.findByText('Live now: Alice')).toBeInTheDocument();
  expect(screen.getByText(/When someone adds you to their room/)).toBeInTheDocument();
  expect(screen.queryByRole('region', { name: 'Friends live now' })).not.toBeInTheDocument();
});
