import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { vi } from 'vitest';
import type { AdminOverview } from '@/api/types.gen';
import { routes } from '@/routes';

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const root = { id: 'u1', handle: 'root', displayName: 'Root' };
const bob = { id: 'u2', handle: 'bob', displayName: 'Bob' };
const overview: AdminOverview = {
  streams: [
    {
      stream: { id: 's1', user: bob, source: 'obs', state: 'live', startedAt: 1, viewers: 3, bitrate: 8e6, lossRatio: 0 },
      roomSlug: 'bobs-room',
      outgoing: 24e6,
    },
  ],
  viewers: 3,
  incoming: 8e6,
  outgoing: 24e6,
  banned: [{ user: { id: 'u3', handle: 'carol', displayName: 'Carol' }, bannedAt: 1_700_000_000 }],
};

function setup(handler: (url: string, init?: RequestInit) => Response | undefined) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const res = handler(url, init);
    if (!res) throw new Error(`unexpected fetch ${init?.method ?? 'GET'} ${url}`);
    return res;
  });
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('confirm', () => true);
  const router = createMemoryRouter(routes, { initialEntries: ['/admin'] });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return fetchMock;
}

test('an admin sees live streams and bandwidth, ends a stream and bans a user', async () => {
  const fetchMock = setup((url, init) => {
    if (url === '/api/me') return json(200, { user: root, admin: true });
    if (url === '/api/admin') return json(200, overview);
    if (init?.method === 'POST' && url === '/api/admin/streams/s1/end') return new Response(null, { status: 204 });
    if (init?.method === 'PUT' && url === '/api/admin/bans/dave') return new Response(null, { status: 204 });
    if (init?.method === 'DELETE' && url === '/api/admin/bans/carol') return new Response(null, { status: 204 });
  });
  expect(await screen.findByRole('link', { name: 'bobs-room' })).toHaveAttribute('href', '/r/bobs-room');
  expect(screen.getAllByText('24.0 Mbit/s')).toHaveLength(2); // the total and the stream
  expect(await screen.findByRole('link', { name: 'Admin' })).toHaveAttribute('href', '/admin');

  fireEvent.click(screen.getByRole('button', { name: 'End' }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/admin/streams/s1/end', expect.objectContaining({ method: 'POST' })));

  fireEvent.change(screen.getByLabelText('Handle'), { target: { value: '@dave' } });
  fireEvent.click(screen.getByRole('button', { name: 'Ban' }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/admin/bans/dave', expect.objectContaining({ method: 'PUT' })));

  fireEvent.click(screen.getByRole('button', { name: 'Lift ban' }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/admin/bans/carol', expect.objectContaining({ method: 'DELETE' })));
});

test('others are turned away and see no admin link', async () => {
  setup((url) => {
    if (url === '/api/me') return json(200, { user: bob });
    if (url === '/api/admin') return json(403, { error: 'only admins may do this', code: 'admin_only' });
  });
  expect(await screen.findByText('This page is for admins.')).toBeInTheDocument();
  expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument();
});
