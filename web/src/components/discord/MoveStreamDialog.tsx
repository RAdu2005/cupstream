import { ArrowRightLeft, X } from 'lucide-react';
import { useState } from 'react';
import { useNavigate } from 'react-router';
import { useAccount } from '@/api/account';
import { ApiError } from '@/api/client';
import { useMoveStream, useStreamViewers } from '@/api/discord';
import { useRoom } from '@/api/rooms';
import { Button, Card } from '@/components/ui';
import { visibilityLabel } from '@/lib/labels';

/**
 * Moves the user's stream from a Discord room into their link room. The
 * video keeps going and the chat stays behind. Viewers are listed ticked:
 * ticked ones may watch until the stream ends (or always), the others are
 * paused.
 */
export function MoveStreamDialog({ streamId, onClose }: { streamId: string; onClose: () => void }) {
  const navigate = useNavigate();
  const account = useAccount();
  const linkRoom = useRoom(account.data?.roomSlug ?? '');
  const viewers = useStreamViewers(streamId, true);
  const move = useMoveStream(streamId);
  const [unticked, setUnticked] = useState<Set<string>>(new Set());
  const [always, setAlways] = useState<Set<string>>(new Set());
  const [openToEveryone, setOpenToEveryone] = useState(false);

  const toggle = (set: Set<string>, id: string) => {
    const next = new Set(set);
    if (!next.delete(id)) next.add(id);
    return next;
  };

  const visibility = linkRoom.data?.visibility;
  const submit = () =>
    move.mutate(
      {
        visibility: openToEveryone ? 'everyone' : '',
        viewers: (viewers.data ?? [])
          .filter((v) => !v.allowed && !unticked.has(v.user.id))
          .map((v) => ({ userId: v.user.id, always: always.has(v.user.id) })),
      },
      {
        onSuccess: (res) => {
          onClose();
          void navigate(`/r/${res.roomSlug}`);
        },
      },
    );

  const opened = openToEveryone || visibility === 'everyone';
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" role="presentation">
      <Card role="dialog" aria-modal aria-labelledby="move-title" className="w-full max-w-md space-y-4">
        <div className="flex items-start justify-between gap-2">
          <h2 id="move-title" className="flex items-center gap-2 text-lg font-bold">
            <ArrowRightLeft className="size-5" aria-hidden /> Move to your link room
          </h2>
          <button aria-label="Close" onClick={onClose} className="rounded p-1 text-muted hover:text-fg">
            <X className="size-4" aria-hidden />
          </button>
        </div>
        <p className="text-sm text-muted">
          Your stream keeps going without interruption and no longer depends on the voice channel. The chat stays in the
          Discord room.
        </p>

        {visibility && visibility !== 'everyone' && (
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={openToEveryone} onChange={(e) => setOpenToEveryone(e.target.checked)} />
            Open to everyone <span className="text-muted">(now: {visibilityLabel[visibility]})</span>
          </label>
        )}

        {!opened && (
          <section aria-label="Viewers" className="space-y-2">
            <p className="text-sm font-semibold">Who keeps watching</p>
            {viewers.data?.length === 0 && <p className="text-sm text-muted">Nobody else is watching right now.</p>}
            <ul className="max-h-60 space-y-1.5 overflow-y-auto">
              {viewers.data?.map((v) => (
                <li key={v.user.id} className="flex items-center justify-between gap-2 text-sm">
                  <label className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={v.allowed || !unticked.has(v.user.id)}
                      disabled={v.allowed}
                      onChange={() => setUnticked((s) => toggle(s, v.user.id))}
                    />
                    {v.user.displayName}
                  </label>
                  {v.allowed ? (
                    <span className="text-xs text-muted">already allowed</span>
                  ) : (
                    <label className="flex items-center gap-1 text-xs text-muted">
                      <input
                        type="checkbox"
                        checked={always.has(v.user.id)}
                        disabled={unticked.has(v.user.id)}
                        onChange={() => setAlways((s) => toggle(s, v.user.id))}
                      />
                      Always allow
                    </label>
                  )}
                </li>
              ))}
            </ul>
            <p className="text-xs text-muted">
              Ticked viewers may watch until the stream ends. The others are paused, and you can still let them in.
            </p>
          </section>
        )}

        {move.error && (
          <p role="alert" className="text-sm text-live">
            {move.error instanceof ApiError ? move.error.message : 'Moving failed.'}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={move.isPending || viewers.isPending} onClick={submit}>
            Move stream
          </Button>
        </div>
      </Card>
    </div>
  );
}

/** Opens MoveStreamDialog; for the streamer's own stream in a Discord room. */
export function MoveStreamButton({ streamId, className }: { streamId: string; className?: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="ghost" className={className ?? 'h-7 px-2'} onClick={() => setOpen(true)}>
        <ArrowRightLeft className="size-3.5" aria-hidden /> Move to my link room
      </Button>
      {open && <MoveStreamDialog streamId={streamId} onClose={() => setOpen(false)} />}
    </>
  );
}
