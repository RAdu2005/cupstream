import { NavLink } from 'react-router-dom';
import { Home, Info, MessageCircle, Radio } from 'lucide-react';
import { cn } from '@/lib/utils';

const items = [
  { to: '/', icon: Home, label: 'Home' },
  { to: '/about', icon: Info, label: 'About' },
];

export function LeftRail() {
  return (
    <aside className="hidden md:flex w-16 lg:w-56 shrink-0 flex-col border-r border-zinc-800 bg-zinc-950 p-2">
      <nav className="flex flex-col gap-1">
        {items.map(({ to, icon: Icon, label }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              cn(
                'flex items-center gap-3 rounded px-2 py-2 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100',
                isActive && 'bg-zinc-800 text-zinc-100',
              )
            }
          >
            <Icon className="h-5 w-5 shrink-0" />
            <span className="hidden lg:inline text-sm">{label}</span>
          </NavLink>
        ))}
        <div className="mt-2 px-2 text-xs uppercase tracking-wider text-zinc-600 hidden lg:block">
          Stream
        </div>
        <div className="flex items-center gap-3 rounded px-2 py-2 text-zinc-400">
          <Radio className="h-5 w-5 shrink-0 text-red-500" />
          <span className="hidden lg:inline text-sm">Live</span>
        </div>
        <div className="flex items-center gap-3 rounded px-2 py-2 text-zinc-400">
          <MessageCircle className="h-5 w-5 shrink-0" />
          <span className="hidden lg:inline text-sm">Chat</span>
        </div>
      </nav>
    </aside>
  );
}
