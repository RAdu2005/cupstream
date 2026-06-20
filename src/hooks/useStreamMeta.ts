import { useQuery } from '@tanstack/react-query';
import type { StreamMeta } from '@/lib/types';

export function useStreamMeta() {
  return useQuery<StreamMeta>({
    queryKey: ['stream', 'meta'],
    queryFn: async () => {
      const res = await fetch('/stream-meta.json', { credentials: 'include' });
      if (!res.ok) throw new Error(`${res.status}`);
      return res.json();
    },
    staleTime: 60_000,
  });
}
