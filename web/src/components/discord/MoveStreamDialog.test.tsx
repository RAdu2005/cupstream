import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { vi } from 'vitest';
import { MoveStreamDialog } from './MoveStreamDialog';

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const user = (id: string, name: string) => ({ id, handle: name.toLowerCase(), displayName: name });

test('moving a stream lets the ticked viewers follow it', async () => {
  let moved: unknown;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/api/account') return json(200, { roomSlug: 'mine' });
      if (url === '/api/rooms/mine')
        return json(200, { id: 'r', slug: 'mine', kind: 'link', visibility: 'invite_only', permissions: {}, streams: [] });
      if (url === '/api/streams/s1/viewers')
        return json(200, [
          { user: user('b', 'Bob'), allowed: false },
          { user: user('c', 'Carol'), allowed: false },
          { user: user('d', 'Dave'), allowed: false },
          { user: user('e', 'Erin'), allowed: true },
        ]);
      if (url === '/api/streams/s1/move' && init?.method === 'POST') {
        moved = JSON.parse(String(init.body));
        return json(200, { roomSlug: 'mine' });
      }
      throw new Error(`unexpected fetch ${url}`);
    }),
  );
  const onClose = vi.fn();
  const router = createMemoryRouter(
    [
      { path: '/r/discord', element: <MoveStreamDialog streamId="s1" onClose={onClose} /> },
      { path: '/r/mine', element: <p>link room</p> },
    ],
    { initialEntries: ['/r/discord'] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );

  const carol = await screen.findByRole('checkbox', { name: 'Carol' });
  expect(carol).toBeChecked();
  expect(screen.getByRole('checkbox', { name: 'Erin' })).toBeDisabled();
  expect(screen.getByText('already allowed')).toBeInTheDocument();
  fireEvent.click(carol);
  fireEvent.click(screen.getAllByRole('checkbox', { name: 'Always allow' })[2]!); // Dave
  fireEvent.click(screen.getByRole('button', { name: 'Move stream' }));

  await waitFor(() => expect(router.state.location.pathname).toBe('/r/mine'));
  expect(moved).toEqual({
    visibility: '',
    viewers: [
      { userId: 'b', always: false },
      { userId: 'd', always: true },
    ],
  });
  expect(onClose).toHaveBeenCalled();
});
