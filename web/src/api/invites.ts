import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './client';
import type { CreateInviteRequest, Invite, InvitePreview, NewInvite, RedeemedInvite } from './types.gen';

const invitesKey = (slug: string) => ['room', slug, 'invites'] as const;

/** The room's invite links that are neither revoked nor expired (owner only). */
export function useInvites(slug: string) {
  return useQuery({
    queryKey: invitesKey(slug),
    queryFn: () => api<Invite[]>(`/api/rooms/${slug}/invites`),
  });
}

export function useCreateInvite(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateInviteRequest) => api<NewInvite>(`/api/rooms/${slug}/invites`, { method: 'POST', json: req }),
    onSuccess: () => qc.invalidateQueries({ queryKey: invitesKey(slug) }),
  });
}

export function useRevokeInvite(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/api/rooms/${slug}/invites/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: invitesKey(slug) }),
  });
}

/** Where an invite link leads. The token goes in the body, so it stays out of logs. */
export function useInvitePreview(token: string) {
  return useQuery({
    queryKey: ['invite', token],
    queryFn: () => api<InvitePreview>('/api/invites/preview', { method: 'POST', json: { token } }),
    retry: false,
    staleTime: Infinity,
  });
}

export function useRedeemInvite() {
  return useMutation({
    mutationFn: (token: string) => api<RedeemedInvite>('/api/invites/redeem', { method: 'POST', json: { token } }),
  });
}
