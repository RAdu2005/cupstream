import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { vi } from 'vitest';
import { routes } from '@/routes';
import { loopbackRedirect } from './AppAuthorize';

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

const user = { id: 'u1', handle: 'alice', displayName: 'Alice' };
const challenge = 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM';
const query = `redirect_uri=${encodeURIComponent('http://127.0.0.1:43210/callback')}&state=s1&code_challenge=${challenge}&code_challenge_method=S256&device_name=Alice%27s%20PC`;

test('accepts only loopback addresses with a port', () => {
  expect(loopbackRedirect('http://127.0.0.1:5000/callback')).not.toBeNull();
  expect(loopbackRedirect('http://[::1]:5000/')).not.toBeNull();
  for (const bad of ['http://127.0.0.1/cb', 'https://127.0.0.1:5000/', 'http://localhost:5000/', 'http://evil.example:80/', null]) {
    expect(loopbackRedirect(bad)).toBeNull();
  }
});

test('sends signed-out visitors to the login page first', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      url === '/api/me' ? json(401, { error: 'sign in required' }) : json(200, { providers: ['discord'] }),
    ),
  );
  const router = renderAt(`/app/authorize?${query}`);
  await waitFor(() => expect(router.state.location.pathname).toBe('/login'));
  expect(new URLSearchParams(router.state.location.search).get('next')).toBe(`/app/authorize?${query}`);
});

test('approving sends the browser back to the app', async () => {
  const assign = vi.fn();
  vi.stubGlobal('location', { ...window.location, assign });
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    if (url === '/api/me') return json(200, { user });
    if (url === '/api/app/authorize') {
      expect(JSON.parse(init?.body as string)).toEqual({
        redirectUri: 'http://127.0.0.1:43210/callback',
        state: 's1',
        codeChallenge: challenge,
        codeChallengeMethod: 'S256',
        deviceName: "Alice's PC",
      });
      return json(200, { redirect: 'http://127.0.0.1:43210/callback?code=c&state=s1' });
    }
    throw new Error(`unexpected fetch ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);

  renderAt(`/app/authorize?${query}`);
  expect(await screen.findByText("Alice's PC")).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
  await waitFor(() => expect(assign).toHaveBeenCalledWith('http://127.0.0.1:43210/callback?code=c&state=s1'));
  expect(await screen.findByText('You are signed in')).toBeInTheDocument();
});

test('cancelling tells the app', async () => {
  const assign = vi.fn();
  vi.stubGlobal('location', { ...window.location, assign });
  vi.stubGlobal('fetch', vi.fn(async () => json(200, { user })));
  renderAt(`/app/authorize?${query}`);
  fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
  expect(assign).toHaveBeenCalledWith('http://127.0.0.1:43210/callback?error=access_denied&state=s1');
});

test('refuses links that would send the code elsewhere', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => json(200, { user })));
  renderAt(`/app/authorize?redirect_uri=${encodeURIComponent('https://evil.example/')}&code_challenge=${challenge}&code_challenge_method=S256`);
  expect(await screen.findByText('This sign-in link is not valid')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Sign in' })).not.toBeInTheDocument();
});
