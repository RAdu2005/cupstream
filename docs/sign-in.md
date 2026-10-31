# Signing in with Google and Discord

The development login needs no setup. To try the real providers locally, create OAuth applications and put their credentials in `.env` (see [.env.example](../.env.example)); `make dev` reads it.

- **Google:** in the Google Cloud console, create an OAuth client of type *Web application* with the redirect URI `http://localhost:5173/api/auth/google/callback`. Scopes: `openid` and `profile`.
- **Discord:** in the Discord developer portal, create an application and add the redirect `http://localhost:5173/api/auth/discord/callback` under OAuth2. Scopes: `identify` and `guilds`.
