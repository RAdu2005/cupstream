import type { Presence } from '@/api/types.gen';

/** Who is in the room right now; people who are live get a badge. */
export function PresenceList({ presence, live }: { presence: Presence; live: Set<string> }) {
  const total = presence.members.length + presence.anonymous;
  return (
    <section aria-label="In this room" className="rounded-xl border border-line bg-surface">
      <h2 className="border-b border-line px-4 py-2 text-sm font-semibold">
        In this room <span className="font-normal text-muted">{total}</span>
      </h2>
      <ul className="max-h-48 space-y-1.5 overflow-y-auto px-4 py-3 text-sm">
        {presence.members.map((u) => (
          <li key={u.id} className="flex items-center gap-2">
            {u.avatarUrl ? (
              <img src={u.avatarUrl} alt="" className="size-5 rounded-full" />
            ) : (
              <span
                aria-hidden
                className="flex size-5 items-center justify-center rounded-full bg-raised text-xs font-semibold"
              >
                {u.displayName.slice(0, 1).toUpperCase()}
              </span>
            )}
            <span className="truncate font-semibold">{u.displayName}</span>
            {live.has(u.id) && <span className="rounded bg-live px-1 text-[10px] font-bold text-white">LIVE</span>}
          </li>
        ))}
        {presence.anonymous > 0 && (
          <li className="text-muted">
            {presence.anonymous === 1 ? '1 guest' : `${presence.anonymous} guests`} without an account
          </li>
        )}
        {total === 0 && <li className="text-muted">Connecting…</li>}
      </ul>
    </section>
  );
}
