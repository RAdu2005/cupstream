# CupStream desktop (companion)

The desktop app's Rust workspace. For now it holds the media pipeline and a command-line prototype; the Tauri app (overview 6.2) will be built on the same crates.

| Crate | What |
|---|---|
| `client/` | The CupStream API as the app uses it: signing in through the system browser (loopback redirect with PKCE), starting and ending streams. |
| `media/` | Capture (xdg-desktop-portal and PipeWire on Wayland, `ximagesrc` on X11, `d3d11screencapturesrc` on Windows), encoder choice with fallbacks, audio without Discord, publishing with `whipclientsink` and a WHEP check. `whipclientsink` and `whepclientsrc` are compiled in from `gst-plugin-webrtc`, so only the system's standard GStreamer plugins are needed. |
| `cli/` | `cupstream-cli`, the prototype. |
| `app/` | The Tauri 2 app: `src-tauri/` (Rust: tray, sign-in, realtime, going live) and `src/` (the window, React, reusing the website's components). |

## Building

Rust is pinned in `mise.toml` here (`mise install` in this directory). The system needs GStreamer 1.24 or later with its development files and the base, good, bad and ugly plugins; on Linux also the PipeWire development files and libclang.

- Arch: `gstreamer gst-plugins-{base,good,bad,ugly} gst-plugin-pipewire gst-plugin-va gst-libav pipewire clang`
- Ubuntu 24.04: see the packages in `.github/workflows/companion.yml`
- Windows: the GStreamer MSVC x86_64 installer from gstreamer.freedesktop.org (full installation, which includes the development files), with `PKG_CONFIG_PATH` set to its `lib\pkgconfig` and its `bin` on `PATH`

```sh
cargo build --release --bin cupstream-cli
```

## Using the prototype

```sh
cupstream-cli encoders                       # which encoders work on this machine
cupstream-cli audio                          # which applications' audio would be captured
cupstream-cli --server http://localhost:5173 sign-in
cupstream-cli --server http://localhost:5173 publish            # your screen, best encoder, audio except Discord
cupstream-cli --server http://localhost:5173 publish --stream-key <key> --source test --audio test
cupstream-cli --server http://localhost:5173 watch --room <slug>  # decode what is live there
```

`publish` options: `--source portal|x11|x11:<xid>|monitor:<n>|window:<hwnd>|test`, `--encoder <element>`, `--codec h264|av1`, `--bitrate <kbit/s>`, `--fps`, `--height`, `--audio except-discord|all|app:<name>|test|none`, `--room <slug>`, `--replace`, `--pick` (ask for the screen again instead of reusing the portal's last choice), `--duration <s>`. The app session token and the portal's restore token are kept in `~/.config/cupstream-cli/` (`%APPDATA%\cupstream-cli` on Windows); the app proper will keep its token in the system keychain.

## The app

```sh
cd app && npm ci
CUPSTREAM_SERVER=http://localhost:5173 npm run tauri dev   # against `make dev`
npm run tauri build -- --bundles deb,rpm                   # nsis on Windows, after the bundle below
```

On Windows, the installer ships its own trimmed GStreamer runtime; build it before `tauri build`, from a GStreamer MSVC installation and the CLI (whose imports are the app's):

```powershell
cargo build --bin cupstream-cli
scripts\gstreamer-bundle.ps1 -GstRoot C:\gstreamer -Exe target\debug\cupstream-cli.exe -Out app\src-tauri\gstreamer
```

The script copies the plugins in `scripts/gstreamer-plugins.txt` and, following their imports with `dumpbin`, exactly the DLLs they and the app need. `tauri.windows.conf.json` installs the DLLs next to `cupstream-app.exe` and the plugins in `gstreamer-1.0\`. At startup, `cupstream_media::use_bundled_gstreamer()` points GStreamer at that directory only, with its own registry in `%LOCALAPPDATA%\CupStream`, so a GStreamer installed system-wide is ignored (a full one also hangs while scanning its plugins). Windows looks for DLLs in the program's directory first, so a GStreamer on `PATH` cannot be loaded instead. `cupstream-cli check` lists the elements the app uses and fails if one is missing; copied into the installation directory, it tests the bundle. A new element needs its plugin added to the list.

Linux also needs the WebKitGTK 4.1, AppIndicator and librsvg development files (Arch: `webkit2gtk-4.1 libayatana-appindicator librsvg`). `CUPSTREAM_SERVER` only sets the server on the first run; afterwards it is in the app's settings.

- **Sign-in** opens the website in the browser (`/app/authorize`); the app session token is kept in the system keychain (Secret Service, Windows Credential Manager).
- **Going live** asks the server for a stream (following the start mode; when no Discord room is possible, or the user is live elsewhere, the window asks first), publishes, and copies the room link. If the connection drops, it publishes again to the same stream a few times before giving up.
- **Realtime:** while running, the app keeps the realtime WebSocket open, so the website lists it (`apps`) and can ask it to go live (`goLive`). While live it follows its room and applies the server's `bitrateTarget` as a ceiling on the chosen bitrate.
- **Tray** (Go live / Stop, Copy room link, Open, Quit); closing the window keeps the app running. **Hotkey** (default Ctrl+Shift+F9) starts and stops; it does not work on Wayland yet. **`cupstream://go-live?room=<slug>`** starts going live; any other `cupstream://` link opens the window.

## How it works

- **Encoding** happens before `whipclientsink`, which only packetizes and sends. Each platform has a list of encoders in order of preference (`media/src/encoder.rs`); the first whose element exists and that encodes a test frame is used. Settings: H.264 without B-frames (High, Main or Constrained Baseline), constant bitrate that can change while live, a 4-second keyframe interval. Viewers' keyframe requests (PLI) reach the encoder, so a joining viewer does not wait for it. AV1 is hardware only.
- **Audio without Discord on Linux** works like venmic: a virtual PipeWire source (`cupstream-audio-<pid>`) that the app records from, and a PipeWire client that links every application's output into it except Discord's. A stream is only linked once its full properties are known; Discord is recognised by the stream's application properties and by its process (Discord's voice engine plays as "WEBRTC VoiceEngine"). Applications keep playing to the speakers as before. Without PipeWire, all system audio is captured, with a warning.
- **Audio without Discord on Windows** uses WASAPI process loopback (`wasapi2src loopback-mode=exclude-process-tree`) with Discord's root process. It needs Windows build 20348 or later; older versions capture all system audio, with a warning. Only one process tree can be left out, and only if Discord is already running when the stream starts.
