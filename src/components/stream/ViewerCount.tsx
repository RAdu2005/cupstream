import { Eye } from 'lucide-react';
import { useViewerCount } from '@/hooks/useViewerCount';
import { useStreamStatus } from '@/hooks/useStreamStatus';
import { formatViewerCount } from '@/lib/utils';

export function ViewerCount() {
  const viewers = useViewerCount();
  const status = useStreamStatus();

  return (
    <div className="flex items-center gap-2 text-sm text-zinc-300">
      {status.data?.isLive && (
        <span className="inline-flex items-center gap-1 rounded bg-red-600 px-2 py-0.5 text-xs font-bold uppercase tracking-wider text-white">
          <span className="h-1.5 w-1.5 rounded-full bg-white" />
          Live
        </span>
      )}
      <span className="inline-flex items-center gap-1 text-zinc-400">
        <Eye className="h-4 w-4" />
        {formatViewerCount(viewers.data ?? 0)} viewer{(viewers.data ?? 0) === 1 ? '' : 's'}
      </span>
    </div>
  );
}
