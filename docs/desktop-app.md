# Desktop app sign-in

The desktop app signs in like a native OAuth client (RFC 8252, with PKCE). It listens on `http://127.0.0.1:<port>/`, opens `/app/authorize?redirect_uri=…&state=…&code_challenge=…&code_challenge_method=S256&device_name=…` in the system browser, and the signed-in user approves it there. The browser is sent back to the app with a one-time code, valid for a minute, which the app exchanges at `POST /api/app/token` for an app session. The app keeps that token in the system keychain and sends it as `Authorization: Bearer` on `/api/` requests, including the realtime WebSocket; `POST /api/auth/logout` with it signs the app out, and the user can revoke it under Settings, Sessions.

While it runs, the app keeps a realtime WebSocket open with that token (no `tab`). Every connection of the same user gets an `apps` event listing the running apps when it connects and whenever the list changes. The website asks an app to go live with the client message `{"type":"appGoLive","id":<app ID, optional>,"room":<slug, optional>}`; the app receives `{"type":"goLive","room":…}` and opens its screen picker, or the website gets an `error` with code `app_offline`.

## Windows installer

The NSIS installer (per user, no administrator rights) includes a trimmed GStreamer runtime: only the plugins the app uses (capture, the encoders, WebRTC, Opus, WASAPI), listed in `companion/scripts/gstreamer-plugins.txt`, and the DLLs they need. The app uses only that runtime, so it works without GStreamer installed and is not disturbed by one that is. CI builds the bundle with `companion/scripts/gstreamer-bundle.ps1`, installs the result and runs `cupstream-cli check` against it; [companion/README.md](../companion/README.md) has the details.
