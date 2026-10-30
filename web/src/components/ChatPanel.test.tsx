import { fireEvent, render, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { MemoryRouter } from 'react-router';
import { vi } from 'vitest';
import type { ChatMessage } from '@/api/types.gen';
import { ChatPanel } from './ChatPanel';

const bob = { id: 'u2', handle: 'bob', displayName: 'Bob' };
const messages: ChatMessage[] = [
  { id: 'm1', user: bob, body: 'hello\nthere', createdAt: 1_800_000_000 },
  { id: 'm2', user: bob, body: '<b>not bold</b>', createdAt: 1_800_000_060 },
];

function renderPanel(props: Partial<ComponentProps<typeof ChatPanel>> = {}) {
  const all = {
    messages,
    signedIn: true,
    canChat: true,
    canModerate: false,
    connected: true,
    error: null,
    onSend: vi.fn(() => true),
    onDelete: vi.fn(),
    ...props,
  };
  const view = render(
    <MemoryRouter>
      <ChatPanel {...all} />
    </MemoryRouter>,
  );
  return { ...all, ...view };
}

test('shows messages as text and sends new ones', () => {
  const { onSend } = renderPanel();
  expect(screen.getByText(/hello\s+there/)).toBeInTheDocument();
  expect(screen.getByText(/<b>not bold<\/b>/)).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: /Delete message/ })).not.toBeInTheDocument();

  const input = screen.getByRole('textbox', { name: 'Message' });
  expect(input).toHaveAttribute('maxLength', '500');
  fireEvent.change(input, { target: { value: '  hi all  ' } });
  fireEvent.submit(input);
  expect(onSend).toHaveBeenCalledWith('hi all');
  expect(input).toHaveValue('');
});

test('keeps the draft when the message could not be sent', () => {
  const { rerender, onDelete } = renderPanel({ onSend: () => false });
  const input = screen.getByRole('textbox', { name: 'Message' });
  fireEvent.change(input, { target: { value: 'hi' } });
  fireEvent.submit(input);
  expect(input).toHaveValue('hi');

  // A message the server refused comes back into an empty box.
  fireEvent.change(input, { target: { value: '' } });
  rerender(
    <MemoryRouter>
      <ChatPanel
        messages={messages}
        signedIn
        canChat
        canModerate={false}
        connected
        error={{ message: 'you are sending messages too quickly', body: 'too fast' }}
        onSend={() => true}
        onDelete={onDelete}
      />
    </MemoryRouter>,
  );
  expect(input).toHaveValue('too fast');
  expect(screen.getByText('you are sending messages too quickly')).toBeInTheDocument();
});

test('moderators can delete messages', () => {
  const { onDelete } = renderPanel({ canModerate: true });
  fireEvent.click(screen.getAllByRole('button', { name: 'Delete message from Bob' })[1]!);
  expect(onDelete).toHaveBeenCalledWith('m2');
});

test('asks visitors to sign in, and explains when chat is not allowed', () => {
  renderPanel({ signedIn: false, canChat: false });
  expect(screen.getByRole('link', { name: 'Sign in' })).toBeInTheDocument();
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  cleanupAndRender({ canChat: false });
  expect(screen.getByText('You cannot chat in this room.')).toBeInTheDocument();
});

function cleanupAndRender(props: Partial<ComponentProps<typeof ChatPanel>>) {
  document.body.innerHTML = '';
  renderPanel(props);
}

test('cannot send while disconnected', () => {
  renderPanel({ connected: false });
  fireEvent.change(screen.getByRole('textbox', { name: 'Message' }), { target: { value: 'hi' } });
  expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled();
});
