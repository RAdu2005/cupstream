import { useQuery } from '@tanstack/react-query';
import { pathsList } from '@/lib/api';

export function useStreamStatus() {
  return useQuery({
    queryKey: ['stream', 'status'],
    queryFn: async () => {
      const list = await pathsList();
      const live = list.items.find((p) => p.name === 'live');
      return {
        isLive: !!live?.ready,
        readyTime: live?.readyTime,
        name: live?.name,
        readers: live?.readers?.length ?? 0,
      };
    },
    refetchInterval: 5_000,
    refetchOnWindowFocus: true,
  });
}
