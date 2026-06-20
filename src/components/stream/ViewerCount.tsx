import { useQuery } from '@tanstack/react-query';
import { Eye } from 'lucide-react';
import { useViewerCount } from '@/hooks/useViewerCount';
import { formatViewerCount } from '@/lib/utils';

export function ViewerCount() {
  const { data } = useViewerCount();
  const { data: status } = useQuery({
    queryKey: ['stream', 'status'],
    queryFn: async () => {
      const r = await fetch('/api/mediamtx/v3/paths/list', { credentials: 'include' });
      if (!r.ok) return { isLive: false };
      const j = await r.json();
      const live = j.items?.find((p: { name: string; ready: boolean }) => p.name === 'live');
      return { isLive: !!live?.ready };
    },
    refetchInterval: 5_000,
  });

  const count = data ?? 0;

  return (
    <div className="flex items-center gap-2 text-sm text-zinc-300">
      {status?.isLive && (
        <span className="inline-flex items-center gap-1 rounded bg-red-600 px-2 py-0.5 text-xs font-bold uppercase tracking-wider text-white">
          <span className="h-1.5 w-1.5 rounded-full bg-white" />
          Live
        </span>
      )}
      <span className="inline-flex items-center gap-1 text-zinc-400">
        <Eye className="h-4 w-4" />
        {formatViewerCount(count)} viewer{count === 1 ? '' : 's'}
      </span>
    </div>
  );
}
