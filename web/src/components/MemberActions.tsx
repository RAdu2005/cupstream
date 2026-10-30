import { MoreHorizontal } from 'lucide-react';
import { useEffect, useRef, useState, type ButtonHTMLAttributes } from 'react';
import { useBan, useKick, useSetAllowed } from '@/api/rooms';
import type { User } from '@/api/types.gen';

/** The room owner's menu for someone in their room: allow, kick or ban. */
export function MemberActions({ slug, user }: { slug: string; user: User }) {
  const [open, setOpen] = useState(false);
  const menu = useRef<HTMLDivElement>(null);
  const allow = useSetAllowed(slug);
  const kick = useKick(slug);
  const ban = useBan(slug);
  const error = allow.error ?? kick.error ?? ban.error;

  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => menu.current?.contains(e.target as Node) || setOpen(false);
    document.addEventListener('mousedown', close);
    return () => document.removeEventListener('mousedown', close);
  }, [open]);

  const act = (run: () => void) => () => {
    setOpen(false);
    run();
  };

  return (
    <div ref={menu} className="relative">
      <button
        aria-label={`Actions for ${user.displayName}`}
        aria-expanded={open}
        className="rounded p-0.5 text-muted hover:bg-raised hover:text-fg"
        onClick={() => setOpen((o) => !o)}
      >
        <MoreHorizontal className="size-4" />
      </button>
      {open && (
        <div role="menu" className="absolute right-0 z-10 mt-1 w-44 rounded-md border border-line bg-surface py-1 shadow-lg">
          <MenuItem onClick={act(() => allow.mutate({ handle: user.handle, canStream: false }))}>
            Add to allowed list
          </MenuItem>
          <MenuItem onClick={act(() => kick.mutate(user.handle))}>Kick</MenuItem>
          <MenuItem
            danger
            onClick={act(() => {
              if (window.confirm(`Ban ${user.displayName}? They cannot enter again, not even with an invite link.`)) {
                ban.mutate(user.handle);
              }
            })}
          >
            Ban
          </MenuItem>
        </div>
      )}
      {error && (
        <span role="alert" className="absolute top-0 right-6 text-xs whitespace-nowrap text-danger">
          {error.message}
        </span>
      )}
    </div>
  );
}

function MenuItem({ danger, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { danger?: boolean }) {
  return (
    <button
      role="menuitem"
      className={`block w-full px-3 py-1.5 text-left text-sm hover:bg-raised ${danger ? 'text-danger' : ''}`}
      {...props}
    />
  );
}
