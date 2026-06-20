import { useQuery } from '@tanstack/react-query';
import { pathGet } from '@/lib/api';

export function useViewerCount() {
  return useQuery({
    queryKey: ['stream', 'viewers'],
    queryFn: async () => {
      try {
        const path = await pathGet('live');
        return path.readers?.length ?? 0;
      } catch {
        return 0;
      }
    },
    refetchInterval: 5_000,
  });
}
