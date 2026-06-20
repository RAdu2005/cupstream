import { useQuery } from '@tanstack/react-query';
import { pathsList } from '@/lib/api';

export function useViewerCount() {
  return useQuery({
    queryKey: ['stream', 'viewers'],
    queryFn: async () => {
      // pathsList returns 200 with items:[] when offline, so no 404 in console
      const res = await pathsList();
      const live = res.items.find((p) => p.name === 'live');
      return live?.readers?.length ?? 0;
    },
    refetchInterval: 5_000,
  });
}
