import { Radio } from 'lucide-react';

export function OfflineScreen() {
  return (
    <div className="flex aspect-video w-full flex-col items-center justify-center gap-3 rounded-lg bg-zinc-900 text-zinc-400">
      <Radio className="h-10 w-10 text-zinc-700" />
      <p className="text-sm">Stream is offline</p>
      <p className="text-xs text-zinc-600">Check back later.</p>
    </div>
  );
}
