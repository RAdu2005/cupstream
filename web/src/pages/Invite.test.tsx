import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { vi } from 'vitest';
import type { Invite, Room } from '@/api/types.gen';
import { describeInvite } from '@/components/InviteLinks';
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
  permissions: { watch: true, stream: true, chat: true, moderateStreams: true, moderateChat: true },
  streams: [],
};

type Handler = (url: string, init?: RequestInit) => Response | undefined;

function setup(path: string, handler: Handler) {
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const res = handler(url, init);
    if (!res) throw new Error(`unexpected fetch ${init?.method ?? 'GET'} ${url}`);
    return res;
  });
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('WebSocket', FakeWebSocket);
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { fetchMock, router };
}

const preview = { roomSlug: 'abc', owner: alice, canStream: false };

test('a signed-out visitor is asked to sign in first', async () => {
  const { fetchMock } = setup('/i/tok123', (url) => {
    if (url === '/api/me') return json(401, { error: 'sign in required', code: 'sign_in_required' });
    if (url === '/api/invites/preview') return json(200, preview);
  });
  expect(await screen.findByText('Alice invited you to their room.')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: 'Sign in' })).toHaveAttribute('href', '/login?next=%2Fi%2Ftok123');
  const [, init] = fetchMock.mock.calls.find(([url]) => url === '/api/invites/preview')!;
  expect(JSON.parse(init!.body as string)).toEqual({ token: 'tok123' });
});

test('a signed-in visitor joins and lands in the room', async () => {
  const { router } = setup('/i/tok123', (url) => {
    if (url === '/api/me') return json(200, { user: bob });
    if (url === '/api/invites/preview') return json(200, preview);
    if (url === '/api/invites/redeem') return json(200, { roomSlug: 'abc' });
    if (url === '/api/rooms/abc') return json(200, { ...room, permissions: { ...room.permissions, moderateStreams: false } });
  });
  await waitFor(() => expect(router.state.location.pathname).toBe('/r/abc'));
});

test('an invalid link says so', async () => {
  setup('/i/old', (url) => {
    if (url === '/api/me') return json(200, { user: bob });
    if (url === '/api/invites/preview') return json(404, { error: 'gone', code: 'invite_invalid' });
  });
  expect(await screen.findByText('This invite link has expired, was revoked or has been used up.')).toBeInTheDocument();
});

test('the owner creates and revokes invite links from the room', async () => {
  let invites: Invite[] = [];
  const created: Invite = { id: 'i1', canStream: true, untilStreamEnd: true, uses: 0, createdAt: 1 };
  const { fetchMock } = setup('/r/abc', (url, init) => {
    const method = init?.method ?? 'GET';
    if (url === '/api/me') return json(200, { user: alice });
    if (url === '/api/rooms/abc') return json(200, room);
    if (url === '/api/rooms/abc/invites' && method === 'GET') return json(200, invites);
    if (url === '/api/rooms/abc/invites' && method === 'POST') {
      invites = [created];
      return json(201, { invite: created, url: 'http://localhost/i/secret' });
    }
    if (url === '/api/rooms/abc/invites/i1' && method === 'DELETE') {
      invites = [];
      return new Response(null, { status: 204 });
    }
  });
  fireEvent.click(await screen.findByRole('button', { name: 'Invite' }));
  const dialog = within(screen.getByRole('dialog', { name: 'Invite people' }));
  expect(await dialog.findByText('No links yet.')).toBeInTheDocument();

  fireEvent.change(dialog.getByRole('combobox'), { target: { value: 'stream' } });
  fireEvent.change(dialog.getByPlaceholderText('No limit'), { target: { value: '5' } });
  fireEvent.click(dialog.getByRole('checkbox'));
  fireEvent.click(dialog.getByRole('button', { name: 'Create link' }));
  expect(await dialog.findByRole('textbox', { name: 'New invite link' })).toHaveValue('http://localhost/i/secret');
  const [, init] = fetchMock.mock.calls.find(([url, i]) => url === '/api/rooms/abc/invites' && i?.method === 'POST')!;
  expect(JSON.parse(init!.body as string)).toEqual({ expiry: 'stream', maxUses: 5, canStream: true });
  expect(await dialog.findByText(/Until the stream ends · 0 uses · can stream/)).toBeInTheDocument();

  fireEvent.click(dialog.getByRole('button', { name: 'Revoke' }));
  expect(await dialog.findByText('No links yet.')).toBeInTheDocument();
});

test('describes invite links', () => {
  const base: Invite = { id: 'i', canStream: false, untilStreamEnd: false, uses: 1, createdAt: 0 };
  expect(describeInvite({ ...base, expiresAt: 1800 }, 0)).toBe('Expires in 30 min · 1 use');
  expect(describeInvite({ ...base, expiresAt: 86400, maxUses: 3 }, 0)).toBe('Expires in 24 h · 1 of 3 uses');
  expect(describeInvite({ ...base, canStream: true }, 0)).toBe('Never expires · 1 use · can stream');
});
