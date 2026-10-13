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

test('development login signs in and returns home', async () => {
  const user = { id: 'u1', handle: 'alice', displayName: 'alice' };
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/me') return json(401, { error: 'sign in required' });
    if (url === '/api/auth/providers') return json(200, { providers: ['dev'] });
    if (url === '/api/auth/dev/login') {
      expect(JSON.parse(init?.body as string)).toEqual({ handle: 'alice' });
      return json(200, { user });
    }
    throw new Error(`unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);

  const router = renderAt('/login');
  fireEvent.change(await screen.findByPlaceholderText(/handle/), { target: { value: 'alice' } });
  fireEvent.click(screen.getByRole('button', { name: 'Sign in as alice' }));

  await waitFor(() => expect(router.state.location.pathname).toBe('/'));
  expect(await screen.findByText('Welcome, alice')).toBeInTheDocument();
});

test('shows a message when no sign-in method is configured', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/me' ? json(401, { error: 'sign in required' }) : json(200, { providers: [] }),
    ),
  );
  renderAt('/login');
  expect(await screen.findByText(/No sign-in methods/)).toBeInTheDocument();
});
