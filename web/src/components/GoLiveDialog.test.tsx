import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { afterEach, expect, test, vi } from 'vitest';
import { broadcast } from '@/lib/broadcast';
import { routes } from '@/routes';
import { fakeScreen } from '@/test/fakeMedia';
import { FakeWebSocket } from '@/test/fakeWebSocket';

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const alice = { id: 'u1', handle: 'alice', displayName: 'Alice' };

function setup({ getDisplayMedia }: { getDisplayMedia?: () => Promise<MediaStream> } = {}) {
  const posts: unknown[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit = {}) => {
      if (url === '/api/streams' && init.method === 'POST') {
        posts.push(JSON.parse(init.body as string));
        return json(409, { error: 'you are not in a Discord voice channel on a server with the CupStream bot', code: 'discord_unavailable' });
      }
      switch (url) {
        case '/api/me':
          return json(200, { user: alice });
        case '/api/account':
          return json(200, {
            user: alice,
            identities: [],
            settings: { startMode: 'discord_auto', obsFallback: 'link' },
            roomSlug: 'alice-room',
          });
        case '/api/account/stream-key':
          return json(200, { hasKey: false, whipUrl: 'http://localhost/whip' });
      }
      return json(404, { error: 'not found' });
    }),
  );
  vi.stubGlobal('WebSocket', FakeWebSocket);
  vi.stubGlobal('navigator', {
    ...navigator,
    userAgent: 'Mozilla/5.0 (X11; Linux x86_64)',
    mediaDevices: getDisplayMedia ? { getDisplayMedia, enumerateDevices: async () => [] } : undefined,
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={createMemoryRouter(routes, { initialEntries: ['/'] })} />
    </QueryClientProvider>,
  );
  return posts;
}

async function openDialog() {
  fireEvent.click(await screen.findByRole('button', { name: 'Go live' }));
  return screen.getByRole('dialog', { name: 'Go live' });
}

afterEach(() => broadcast.answer(false));

test('the dialog offers the app first, then the browser, then OBS collapsed', async () => {
  setup({ getDisplayMedia: async () => fakeScreen() });
  const dialog = await openDialog();
  const headings = within(dialog).getAllByRole('heading', { level: 3 }).map((h) => h.textContent);
  expect(headings).toEqual(['CupStream appRecommended', 'Stream from this browser']);
  expect(within(dialog).getByRole('button', { name: /Download for Linux/ })).toBeDisabled();

  const obs = within(dialog).getByText('OBS (advanced)').closest('details')!;
  expect(obs.open).toBe(false);
  expect(await within(obs).findByText('http://localhost/whip')).toBeInTheDocument();
  expect(within(obs).getByRole('combobox')).toHaveValue('discord_auto');

  fireEvent.keyDown(window, { key: 'Escape' });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

test('a browser that cannot capture the screen is told so', async () => {
  setup();
  const dialog = await openDialog();
  expect(within(dialog).getByText(/cannot share its screen/)).toBeInTheDocument();
  expect(within(dialog).queryByRole('button', { name: 'Choose what to share' })).not.toBeInTheDocument();
});

test('capturing shows what was captured, its warnings and the sound choice, then goes live', async () => {
  const captured = fakeScreen({ audio: true });
  const getDisplayMedia = vi.fn(async () => captured);
  const posts = setup({ getDisplayMedia });
  const dialog = await openDialog();
  expect(within(dialog).getByRole('option', { name: /AV1/ })).toBeDisabled(); // jsdom sends no AV1

  fireEvent.click(within(dialog).getByRole('button', { name: 'Choose what to share' }));
  await within(dialog).findByLabelText('Preview');
  expect(getDisplayMedia).toHaveBeenCalledWith(expect.objectContaining({ systemAudio: 'include' }));
  expect(within(dialog).getByText(/Sharing a screen/)).toHaveTextContent('1920×1080, 30 fps');
  expect(within(dialog).getByText(/including your Discord call/)).toBeInTheDocument();
  expect(within(dialog).getByLabelText('Sound')).toHaveValue('screen');
  expect(within(dialog).getByLabelText('Resolution')).toBeDisabled();

  fireEvent.click(within(dialog).getByRole('button', { name: 'Go live' }));
  const ask = await screen.findByRole('button', { name: 'Stream to my link room' });
  expect(posts).toEqual([{ source: 'web' }]);
  expect(screen.getByText('You are not in a Discord voice channel on a server with the CupStream bot.')).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(captured.getTracks().every((t) => t.readyState === 'ended')).toBe(true));
  expect(ask).not.toBeInTheDocument();
});

test('closing the dialog stops a capture that never went live', async () => {
  const captured = fakeScreen();
  setup({ getDisplayMedia: async () => captured });
  const dialog = await openDialog();
  fireEvent.click(within(dialog).getByRole('button', { name: 'Choose what to share' }));
  await within(dialog).findByLabelText('Preview');
  expect(within(dialog).getByText(/shares no sound/)).toBeInTheDocument();
  fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
  expect(captured.getVideoTracks()[0]!.readyState).toBe('ended');
});
