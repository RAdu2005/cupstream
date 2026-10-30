# CupStream

Low-latency live streaming for friends: go live from the desktop app, a browser or OBS, and watch together on the website. Rooms are tied to a Discord voice channel or shared through links.

The design and the implementation plan are in [output/md/cupstream-project-overview.md](output/md/cupstream-project-overview.md).

## Layout

| Path | What |
|---|---|
| `server/` | Go server: OAuth sign-in and accounts, rooms and access checks (`internal/access`), the WHIP/WHEP media gateway (`internal/gateway`), the realtime WebSocket with presence and chat (`internal/realtime`), and later the Discord bot. SQLite with goose migrations and sqlc queries. |
| `web/` | Website: React, TypeScript, Vite, Tailwind CSS, TanStack Query, React Router. |
| `deploy/mediamtx/` | MediaMTX image, built from [our fork](https://github.com/RAdu2005/mediamtx) at a pinned tag, and its configuration. |
| `deploy/cupstream/` | systemd and OpenRC service files, and `cupstream-deploy`, which CI runs on the production server. |

## Development

Requirements: [mise](https://mise.jdx.dev) (installs the pinned Go, Node and sqlc from `mise.toml`) and Docker.

```sh
mise install
make dev
```

`make dev` starts MediaMTX in Docker, the Go server on `127.0.0.1:8080` and the Vite dev server. Open http://localhost:5173 and use the development login, which signs in as any handle without a password. It is enabled only by `CUPSTREAM_DEV_LOGIN` and only works on localhost.

| Command | |
|---|---|
| `make test` | Go vet and tests (with `-race`), website typecheck and tests |
| `make generate` | Regenerate sqlc queries (`server/internal/store/*.gen.go`) and TypeScript API types (`web/src/api/types.gen.ts`) |
| `make build` | `bin/cupstream`, a single binary with the website embedded |
| `make images` | Docker images for CupStream and MediaMTX |
| `make tidy` | Fast-forward the main checkout's `master` and delete local branches whose pull request was merged; `scripts/tidy.sh --install` runs it every 15 minutes |

Database changes go in a new file in `server/internal/store/migrations/`, with queries in `server/internal/store/queries/`; run `make generate` afterwards. In a query, use either `?` or `sqlc.arg(name)` for all parameters, never both: sqlc numbers mixed parameters wrongly. API types live in `server/internal/api/` so the website types can be generated from them.

### Signing in with Google and Discord

The development login needs no setup. To try the real providers locally, create OAuth applications and put their credentials in `.env` (see [.env.example](.env.example)); `make dev` reads it.

- **Google:** in the Google Cloud console, create an OAuth client of type *Web application* with the redirect URI `http://localhost:5173/api/auth/google/callback`. Scopes: `openid` and `profile`.
- **Discord:** in the Discord developer portal, create an application and add the redirect `http://localhost:5173/api/auth/discord/callback` under OAuth2. Scopes: `identify` and `guilds`.

### Realtime

Each browser tab opens one WebSocket, `GET /api/realtime?tab=<id>`, and subscribes to rooms over it; the messages are the `ClientMessage` and event types in `server/internal/api`. It carries live streams and viewer counts, presence, chat (kept 24 hours) and, for streamers, their viewers' statistics, so the room page does not poll. Only pages on `CUPSTREAM_PUBLIC_URL`'s origin may connect, and the server pings every 30 seconds so Cloudflare does not close idle connections. WHEP requests pass the same tab ID; when a tab's WebSocket has been gone for 15 seconds, the media sessions it opened are closed.

### Streaming locally

In your room's settings, generate a stream key. In OBS, choose the **WHIP** service with the server `http://localhost:5173/whip` and the key as the Bearer token; a co-streamer adds `?room=<slug>` to stream into someone else's room. Set OBS's keyframe interval to 1–2 seconds: OBS ignores requests for keyframes, so a new viewer waits up to one interval for the picture (browsers and the desktop app answer them, so their viewers start almost at once). The room API reports each stream's measured `keyFrameInterval`. Without OBS, ffmpeg 8 or later can publish a test pattern, but its WHIP client rejects ICE-over-TCP candidates, so run MediaMTX with `MTX_WEBRTCLOCALTCPADDRESS=` for that.

## Deployment

Every merge into `master` is deployed to https://cupstream.larp.love once CI passes; `/healthz` reports the running commit. [AGENTS.md](AGENTS.md) has the rules for branches, pull requests, deploys and rollbacks.

MediaMTX is installed by hand, as a systemd service next to `cupstream.service`, and only changes when its pinned tag does. Take the binary from the image, so production runs the same build as development and CI:

```sh
docker compose build mediamtx
id=$(docker create cupstream-mediamtx:<tag>) && docker cp $id:/usr/local/bin/mediamtx . && docker rm $id
```

On the server: install it as `/usr/local/bin/mediamtx`, with a system user `mediamtx`, [mediamtx.yml](deploy/mediamtx/mediamtx.yml) as `/etc/mediamtx/mediamtx.yml`, [env.example](deploy/mediamtx/env.example) adapted as `/etc/mediamtx/env` (both `root:mediamtx`, mode 640) and [mediamtx.service](deploy/mediamtx/mediamtx.service) in `/etc/systemd/system/`. Open port 8189 (UDP and TCP) in the firewall, raise the UDP send buffer limit (see [MediaMTX build](#mediamtx-build)), then restart `mediamtx` and `cupstream`: the server reads MediaMTX's features at startup, and `/healthz` should report `"pause":true,"events":true`.

## Configuration

The server reads `CUPSTREAM_*` environment variables; see [.env.example](.env.example). MediaMTX's HTTP API and WHIP/WHEP listener are bound to `127.0.0.1`: only the CupStream server talks to them, and media flows on port 8189 (UDP, with TCP fallback).

## MediaMTX build

MediaMTX runs from the fork [RAdu2005/mediamtx](https://github.com/RAdu2005/mediamtx): an upstream release plus the WHEP pause/resume pull request [bluenviron/mediamtx#6308](https://github.com/bluenviron/mediamtx/pull/6308) and our changes (overview, section 3.2). The tag is pinned in `deploy/mediamtx/Dockerfile` and `compose.yaml`; the fork's `CUPSTREAM.md` describes how to upgrade.

The server reads the build's version from MediaMTX at startup. From `v1.21.2-cupstream.2` on, it follows MediaMTX's event stream (`/v3/events`) and pauses viewers through the API; with any other build it falls back to polling every 2 seconds and to closing sessions instead of pausing them. `/healthz` reports which is in use.

Viewers pause and resume their own sessions with `POST /api/media/{stream}/whep/{secret}/pause` and `.../resume` (optional body `{"video": bool, "audio": bool}`), and open pre-warmed sessions with `POST /api/streams/{id}/whep?paused=true&preview=false`. A room holds up to 8 streams and a viewer receives up to 4 at once (a signed-in user across their tabs; paused sessions do not count); beyond that the server answers 409 with `room_full` or `too_many_streams`. A viewer who loses access is held: their media is paused, they cannot resume it, it comes back if they regain access, and the session is closed after 2 minutes.

On the host, raise the UDP send buffer limit to match MediaMTX's `udpWriteBufferSize` (8 MiB), or Linux silently caps it:

```sh
echo 'net.core.wmem_max = 8388608' | sudo tee /etc/sysctl.d/90-cupstream.conf
sudo sysctl --system
```
