import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, ApiError } from './client';
import type { FriendSuggestion, Friends, LiveFriend, User, UserRelation } from './types.gen';

/** Every friends query starts with this key; realtime events invalidate it. */
export const friendsKey = ['friends'] as const;

/** Friends and pending requests, kept current by realtime events (Notifications). */
export function useFriends(enabled = true) {
  return useQuery({
    queryKey: [...friendsKey, 'list'],
    queryFn: () => api<Friends>('/api/friends'),
    enabled,
    staleTime: 60_000,
  });
}

/** Friends' streams in rooms the user may enter, oldest first. */
export function useFriendsLive(enabled = true) {
  return useQuery({
    queryKey: [...friendsKey, 'live'],
    queryFn: () => api<LiveFriend[]>('/api/friends/live'),
    enabled,
  });
}

export function useSuggestions(enabled: boolean) {
  return useQuery({
    queryKey: [...friendsKey, 'suggestions'],
    queryFn: () => api<FriendSuggestion[]>('/api/friends/suggestions'),
    enabled,
  });
}

export function useBlocked() {
  return useQuery({ queryKey: [...friendsKey, 'blocked'], queryFn: () => api<User[]>('/api/friends/blocked') });
}

/** Finds someone by exact handle: null when nobody has it. */
export function useLookup(handle: string) {
  return useQuery({
    queryKey: [...friendsKey, 'lookup', handle],
    enabled: handle !== '',
    retry: false,
    queryFn: async () => {
      try {
        return await api<UserRelation>(`/api/friends/lookup?handle=${encodeURIComponent(handle)}`);
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) return null;
        throw err;
      }
    },
  });
}

function useFriendMutation<T>(fn: (userId: string) => Promise<T>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSettled: () => qc.invalidateQueries({ queryKey: friendsKey }),
  });
}

/** Sends a friend request, or accepts theirs if they already asked. */
export function useSendRequest() {
  return useFriendMutation((userId) => api<UserRelation>('/api/friends/requests', { method: 'POST', json: { userId } }));
}

export function useAcceptRequest() {
  return useFriendMutation((userId) => api<UserRelation>(`/api/friends/requests/${userId}/accept`, { method: 'POST' }));
}

/** Declines a request to the user, or cancels theirs. */
export function useRemoveRequest() {
  return useFriendMutation((userId) => api<void>(`/api/friends/requests/${userId}`, { method: 'DELETE' }));
}

export function useUnfriend() {
  return useFriendMutation((userId) => api<void>(`/api/friends/${userId}`, { method: 'DELETE' }));
}

export function useDismissSuggestion() {
  return useFriendMutation((userId) => api<void>(`/api/friends/suggestions/${userId}`, { method: 'DELETE' }));
}

export function useBlock() {
  return useFriendMutation((userId) => api<UserRelation>(`/api/friends/blocked/${userId}`, { method: 'PUT' }));
}

export function useUnblock() {
  return useFriendMutation((userId) => api<void>(`/api/friends/blocked/${userId}`, { method: 'DELETE' }));
}
