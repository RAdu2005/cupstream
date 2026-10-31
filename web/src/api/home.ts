import { useQuery } from '@tanstack/react-query';
import { api } from './client';
import type { Home } from './types.gen';

/** The home page's rooms. Live states change, so it is fetched again every 15 seconds. */
export function useHome(enabled: boolean) {
  return useQuery({
    queryKey: ['home'],
    queryFn: () => api<Home>('/api/home'),
    enabled,
    refetchInterval: 15_000,
    refetchOnWindowFocus: true,
  });
}
