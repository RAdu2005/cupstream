# Signing in with Google and Discord

The development login needs no setup. To try the real providers locally, create OAuth applications and put their credentials in `.env` (see [.env.example](../.env.example)); `make dev` reads it.

- **Google:** in the Google Cloud console, create an OAuth client of type *Web application* with the redirect URI `http://localhost:5173/api/auth/google/callback`. Scopes: `openid` and `profile`.
- **Discord:** in the Discord developer portal, create an application and add the redirect `http://localhost:5173/api/auth/discord/callback` under OAuth2. Scopes: `identify` and `guilds`.

## Handles

A new user who signs in with Google or Discord has no account yet: the callback keeps their identity in a sealed cookie (30 minutes) and sends them to `/signup`, where they choose a handle (suggested from their username at the provider) and a display name. `POST /api/auth/signup` creates the account and signs them in. The development login creates the account at once, with the handle typed in.

A handle is 2–32 lowercase letters, digits and underscores, and it names the user's link room: `/r/<handle>`. It may change once a week. For a day after a change, the handle given up stays reserved to its owner, and switching back to it undoes the change, which then does not count towards the week. When a handle changes, the room's old link stops working; the people in the room follow it to the new one (the realtime event `roomRenamed`).
