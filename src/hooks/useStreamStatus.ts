import { useQuery } from '@tanstack/react-query';
import { pathsList, pathGet } from '@/lib/api';

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
      };
    },
    refetchInterval: 5_000,
    refetchOnWindowFocus: true,
  });
}

export function useStreamInfo() {
  return useQuery({
    queryKey: ['stream', 'info'],
    queryFn: () => pathGet('live'),
    refetchInterval: 10_000,
  });
}
