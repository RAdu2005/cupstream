# Desktop app sign-in

The desktop app signs in like a native OAuth client (RFC 8252, with PKCE). It listens on `http://127.0.0.1:<port>/`, opens `/app/authorize?redirect_uri=…&state=…&code_challenge=…&code_challenge_method=S256&device_name=…` in the system browser, and the signed-in user approves it there. The browser is sent back to the app with a one-time code, valid for a minute, which the app exchanges at `POST /api/app/token` for an app session. The app keeps that token in the system keychain and sends it as `Authorization: Bearer` on `/api/` requests, including the realtime WebSocket; `POST /api/auth/logout` with it signs the app out, and the user can revoke it under Settings, Sessions.

While it runs, the app keeps a realtime WebSocket open with that token (no `tab`). Every connection of the same user gets an `apps` event listing the running apps when it connects and whenever the list changes. The website asks an app to go live with the client message `{"type":"appGoLive","id":<app ID, optional>,"room":<slug, optional>}`; the app receives `{"type":"goLive","room":…}` and opens its screen picker, or the website gets an `error` with code `app_offline`.

## Choosing what to share

On Windows, going live (from the window, the tray, the hotkey or the website's `goLive`) first shows the app's picker: the screens, then the open windows, each with a thumbnail of what viewers will see, taken with the same Windows.Graphics.Capture the stream uses. Tool windows, hidden (cloaked) and minimised windows, the desktop shell and the app's own windows are left out. The choice is remembered for the next stream. On Linux the system's screen-sharing dialog asks instead (xdg-desktop-portal), with its own restore token. `cupstream-cli sources --thumbnails <dir>` lists the same sources, with the IDs `publish --source` takes.

## Windows installer

The NSIS installer (per user, no administrator rights) includes a trimmed GStreamer runtime: only the plugins the app uses (capture, the encoders, WebRTC, Opus, WASAPI), listed in `companion/scripts/gstreamer-plugins.txt`, and the DLLs they need. The app uses only that runtime, so it works without GStreamer installed and is not disturbed by one that is. CI builds the bundle with `companion/scripts/gstreamer-bundle.ps1`, installs the result and runs `cupstream-cli check` against it; [companion/README.md](../companion/README.md) has the details.

## Low-quality version

When `POST /api/streams` answers `lowQuality.publish: true` and the streamer has *Send a low-quality version* on (the default, in the app's settings), the app also publishes a second, smaller version of the stream for viewers on slow connections ([low-quality versions](low-quality.md)): 720p at 30 fps and 1.2 Mbit/s, or 480p at 0.8 Mbit/s when the stream itself is 720p or smaller, H.264 without B-frames, with the same Opus audio. It is a second encode of the same capture, published with WHIP at the stream's `whipUrl` plus `?quality=low` once the stream's own session is connected.

It never costs the stream itself: its queue holds one frame and drops the rest when its encoder falls behind, and an error in it removes only it. It uses the stream's hardware encoder when there is one (a second session on the GPU), and OpenH264 or x264 otherwise or when the GPU refuses another session; after a failure it is tried again, up to five times. The app shows whether it is being sent while live; `cupstream-cli publish` sends it too (`--no-low` to skip it) and `cupstream-cli watch --low` plays it.
