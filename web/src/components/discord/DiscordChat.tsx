import { ChevronDown, ChevronUp, MessageSquare } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import type { Room } from '@/api/types.gen';

/**
 * The chat of a Discord room is collapsed by default: people there talk in
 * voice. Link rooms show it as is.
 */
export function DiscordChat({ room, count, children }: { room: Room; count: number; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  if (room.kind !== 'discord') return children;
  return (
    <div className="flex flex-col gap-2">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="flex items-center justify-between rounded-xl border border-line bg-surface px-4 py-2 text-sm font-semibold hover:bg-raised"
      >
        <span className="flex items-center gap-2">
          <MessageSquare className="size-4" aria-hidden /> Chat
          {count > 0 && <span className="font-normal text-muted">{count}</span>}
        </span>
        {open ? <ChevronUp className="size-4" aria-hidden /> : <ChevronDown className="size-4" aria-hidden />}
      </button>
      {open && children}
    </div>
  );
}
