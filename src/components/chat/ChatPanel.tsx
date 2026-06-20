import { useEffect, useRef, useState } from 'react';
import { Send } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { connectChat, type ChatConnection, type ChatStatus } from '@/lib/chat';
import { authStatus } from '@/lib/api';
import { useQuery } from '@tanstack/react-query';
import type { ChatMessage } from '@/lib/types';
import { cn } from '@/lib/utils';

export function ChatPanel() {
  const { data: auth } = useQuery({ queryKey: ['auth'], queryFn: authStatus });
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [text, setText] = useState('');
  const [status, setStatus] = useState<ChatStatus>('connecting');
  const connRef = useRef<ChatConnection | null>(null);
  const listRef = useRef<HTMLUListElement | null>(null);
  const myName = auth?.authenticated ? auth.username : '';

  useEffect(() => {
    if (!auth?.authenticated) return;
    const conn = connectChat({
      onHistory: (m) => setMessages(m ?? []),
      onMessage: (m) => setMessages((prev) => [...prev, m]),
      onError: (e) => console.warn('chat error:', e),
      onStatus: setStatus,
    });
    connRef.current = conn;
    return () => {
      conn.close();
      connRef.current = null;
    };
  }, [auth?.authenticated]);

  useEffect(() => {
    const el = listRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
  }, [messages?.length ?? 0]);

  function send() {
    const t = text.trim();
    if (!t || status !== 'open') return;
    connRef.current?.send(t);
    setText('');
  }

  return (
    <div className="flex h-full min-h-0 flex-col bg-zinc-950">
      <div className="flex h-9 items-center justify-between border-b border-zinc-800 px-3 text-xs uppercase tracking-wider text-zinc-500">
        <span>Stream Chat</span>
        <span
          className={cn(
            'inline-flex items-center gap-1',
            status === 'open' && 'text-emerald-500',
            status === 'connecting' && 'text-amber-500',
            status === 'closed' && 'text-red-500',
          )}
        >
          <span className="h-1.5 w-1.5 rounded-full bg-current" />
          {status}
        </span>
      </div>
      <ul
        ref={listRef}
        className="min-h-0 flex-1 space-y-1 overflow-y-auto p-3 text-sm"
      >
        {messages.length === 0 && (
          <li className="text-zinc-600 italic">No messages yet. Say hi.</li>
        )}
        {messages.map((m) => (
          <li key={m.id} className="leading-snug">
            <span
              className={cn(
                'mr-1.5 font-medium',
                m.username === myName ? 'text-brand' : 'text-zinc-400',
              )}
            >
              {m.username}
            </span>
            <span className="text-zinc-200 whitespace-pre-wrap break-words">
              {m.text}
            </span>
          </li>
        ))}
      </ul>
      <div className="flex gap-2 border-t border-zinc-800 p-2">
        <Input
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault();
              send();
            }
          }}
          maxLength={500}
          disabled={status !== 'open'}
          placeholder={status === 'open' ? `Chatting as ${myName}` : 'Connecting…'}
          className="flex-1"
        />
        <Button
          onClick={send}
          disabled={status !== 'open' || !text.trim()}
          size="sm"
        >
          <Send className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
}
