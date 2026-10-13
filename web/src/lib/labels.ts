import type { AuthProvider, Visibility } from '@/api/types.gen';

export const visibilityLabel: Record<Visibility, string> = {
  everyone: 'Everyone with the link',
  signed_in: 'Signed-in users with the link',
  invite_only: 'Invite only',
};

export const providerLabel: Record<AuthProvider, string> = {
  google: 'Google',
  discord: 'Discord',
  dev: 'Development login',
};

/** Messages for the ?error= codes of OAuth redirects. */
export const oauthErrors: Record<string, string> = {
  expired: 'The sign-in took too long or was interrupted. Please try again.',
  cancelled: 'Sign-in was cancelled.',
  provider_error: 'The provider did not confirm your sign-in. Please try again.',
  provider_unavailable: 'This sign-in method is unavailable right now.',
  session_changed: 'You were signed out while linking. Sign in and try again.',
  provider_linked: 'An account of this provider is already linked. Unlink it first.',
  internal: 'Something went wrong on our side. Please try again.',
};
