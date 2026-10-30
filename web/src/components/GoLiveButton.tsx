import { Radio } from 'lucide-react';
import { useState } from 'react';
import { GoLiveDialog, useBroadcast } from './GoLiveDialog';
import { Button } from './ui';

/** Opens the go-live dialog; while streaming it shows the stream's state. */
export function GoLiveButton() {
  const [open, setOpen] = useState(false);
  const state = useBroadcast();

  let label = 'Go live';
  let variant: 'primary' | 'secondary' = 'primary';
  switch (state.phase) {
    case 'starting':
      label = 'Starting…';
      break;
    case 'live':
      label = `Live · ${state.viewers}`;
      break;
    case 'reconnecting':
      label = 'Reconnecting…';
      variant = 'secondary';
      break;
    case 'ended':
      label = 'Stream ended';
      variant = 'secondary';
      break;
  }
  return (
    <>
      <Button
        variant={variant}
        className={state.phase === 'live' ? 'bg-live hover:bg-live/80' : undefined}
        onClick={() => setOpen(true)}
        title={state.phase === 'live' ? `${state.viewers} watching` : undefined}
      >
        <Radio className="size-4" aria-hidden />
        <span className="hidden sm:inline">{label}</span>
      </Button>
      {open && <GoLiveDialog onClose={() => setOpen(false)} />}
    </>
  );
}
