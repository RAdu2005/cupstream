import { UserPlus } from 'lucide-react';
import { useState } from 'react';
import { Dialog } from './Dialog';
import { InviteLinks } from './InviteLinks';
import { Button } from './ui';

/** The owner's button for inviting people to their link room. */
export function ManageRoom({ slug }: { slug: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="secondary" onClick={() => setOpen(true)}>
        <UserPlus className="size-4" aria-hidden /> Invite
      </Button>
      {open && (
        <Dialog title="Invite people" onClose={() => setOpen(false)}>
          <InviteLinks slug={slug} />
        </Dialog>
      )}
    </>
  );
}
