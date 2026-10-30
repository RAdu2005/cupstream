import { Send, Trash2 } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type FormEvent } from 'react';
import { Link } from 'react-router';
import type { ChatError } from '@/api/realtime';
import type { ChatMessage } from '@/api/types.gen';
import { Button, Input } from './ui';

/** The longest message the server accepts, in characters. */
const MAX_BODY = 500;

type Props = {
  messages: ChatMessage[];
  signedIn: boolean;
  canChat: boolean;
  canModerate: boolean;
  connected: boolean;
  error: ChatError | null;
  /** Sends a message; false if it could not be sent. */
  onSend: (body: string) => boolean;
  onDelete: (id: string) => void;
};

const timeFormat = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' });

export function ChatPanel({ messages, signedIn, canChat, canModerate, connected, error, onSend, onDelete }: Props) {
  const [draft, setDraft] = useState('');
  const list = useRef<HTMLOListElement>(null);
  const pinned = useRef(true);

  // Stay at the newest message unless the reader scrolled up.
  useLayoutEffect(() => {
    const el = list.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [messages]);

  // A refused message comes back into the box, unless something new was typed.
  useEffect(() => {
    if (error?.body) setDraft((d) => d || error.body!);
  }, [error]);

  function submit(e: FormEvent) {
    e.preventDefault();
    const body = draft.trim();
    if (body && onSend(body)) setDraft('');
  }

  return (
    <section
      aria-label="Chat"
      className="flex min-h-80 flex-col rounded-xl border border-line bg-surface lg:min-h-0 lg:flex-1"
    >
      <h2 className="border-b border-line px-4 py-2 text-sm font-semibold">Chat</h2>
      <ol
        ref={list}
        onScroll={(e) => {
          const el = e.currentTarget;
          pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
        className="flex-1 space-y-2 overflow-y-auto px-4 py-3 text-sm lg:max-h-[60vh]"
      >
        {messages.length === 0 && <li className="text-muted">No messages yet. Messages disappear after 24 hours.</li>}
        {messages.map((m) => (
          <li key={m.id} className="group flex items-start gap-2">
            <p className="min-w-0 flex-1 break-words whitespace-pre-wrap">
              <time className="mr-1.5 text-xs text-muted" dateTime={new Date(m.createdAt * 1000).toISOString()}>
                {timeFormat.format(m.createdAt * 1000)}
              </time>
              <span className="font-semibold" title={`@${m.user.handle}`}>
                {m.user.displayName}
              </span>
              : {m.body}
            </p>
            {canModerate && (
              <button
                className="text-muted opacity-0 group-hover:opacity-100 hover:text-danger focus-visible:opacity-100"
                aria-label={`Delete message from ${m.user.displayName}`}
                title="Delete message"
                onClick={() => onDelete(m.id)}
              >
                <Trash2 className="size-3.5" />
              </button>
            )}
          </li>
        ))}
      </ol>
      <div className="border-t border-line p-3">
        {!signedIn ? (
          <p className="text-sm text-muted">
            <Link
              to={`/login?next=${encodeURIComponent(window.location.pathname)}`}
              className="font-semibold text-accent hover:underline"
            >
              Sign in
            </Link>{' '}
            to chat.
          </p>
        ) : !canChat ? (
          <p className="text-sm text-muted">You cannot chat in this room.</p>
        ) : (
          <form onSubmit={submit} className="flex gap-2">
            <Input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              maxLength={MAX_BODY}
              placeholder="Send a message"
              aria-label="Message"
            />
            <Button type="submit" className="px-3" disabled={!connected || !draft.trim()} aria-label="Send">
              <Send className="size-4" aria-hidden />
            </Button>
          </form>
        )}
        {error && signedIn && <p className="mt-2 text-xs text-danger">{error.message}</p>}
      </div>
    </section>
  );
}
