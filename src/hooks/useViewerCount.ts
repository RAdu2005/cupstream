import { useQuery } from '@tanstack/react-query';
import { hlsMuxersList } from '@/lib/api';

export function useViewerCount() {
  return useQuery({
    queryKey: ['stream', 'viewers'],
    queryFn: async () => {
      const res = await hlsMuxersList();
      const total = res.items
        .filter((m) => m.name === 'live')
        .reduce((acc, m) => acc + (m.readers?.length ?? 0), 0);
      return total;
    },
    refetchInterval: 5_000,
  });
}
