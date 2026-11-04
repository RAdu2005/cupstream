import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { vi } from 'vitest';
import { routes } from '@/routes';

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function renderAt(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

const pending = { provider: 'discord', username: 'ana.p', displayName: 'Ana', handle: 'ana_p' };

test('a new user chooses a free handle and continues where they were going', async () => {
  const user = { id: 'u1', handle: 'ana', displayName: 'Ana P.' };
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/me') return json(401, { error: 'sign in required' });
    if (url === '/api/auth/signup' && !init?.method) return json(200, pending);
    if (url === '/api/auth/handle?handle=mara') return json(200, { available: false, error: 'this handle is taken' });
    if (url === '/api/auth/handle?handle=ana') return json(200, { available: true });
    if (url === '/api/auth/signup' && init?.method === 'POST') {
      expect(JSON.parse(init.body as string)).toEqual({ handle: 'ana', displayName: 'Ana P.' });
      return json(200, { user });
    }
    if (url === '/api/rooms/mara') return json(200, { id: 'r1', slug: 'mara', kind: 'link', permissions: {}, streams: [] });
    return json(404, { error: 'not found' });
  });
  vi.stubGlobal('fetch', fetchMock);

  const router = renderAt('/signup?next=/friends');
  const handle = await screen.findByRole('textbox', { name: 'Handle' });
  expect(handle).toHaveValue('ana_p');
  expect(screen.getByText('Signed in with Discord as ana.p.')).toBeInTheDocument();
  expect(screen.getByText(/\/r\/ana_p$/)).toBeInTheDocument();

  fireEvent.change(handle, { target: { value: 'Mara' } });
  expect(await screen.findByText('this handle is taken')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Create account' })).toBeDisabled();

  fireEvent.change(handle, { target: { value: 'ana' } });
  fireEvent.change(screen.getByRole('textbox', { name: 'Display name' }), { target: { value: 'Ana P.' } });
  await waitFor(() => expect(screen.getByRole('button', { name: 'Create account' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'Create account' }));
  await waitFor(() => expect(router.state.location.pathname).toBe('/friends'));
});

test('an expired sign-up sends the user back to sign in', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/me' ? json(401, { error: 'sign in required' }) : json(404, { error: 'no sign-up is pending' }),
    ),
  );
  renderAt('/signup?next=/r/abc');
  expect(await screen.findByText('Sign-up expired')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Sign in' })).toHaveAttribute('href', '/login?next=%2Fr%2Fabc');
});
