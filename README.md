# CupStream

Low-latency live streaming for friends: go live from the desktop app, a browser or OBS, and watch together on the website. Rooms are tied to a Discord voice channel or shared through links.

The design and the implementation plan are in [output/md/cupstream-project-overview.md](output/md/cupstream-project-overview.md).

## Layout

| Path | What |
|---|---|
| `server/` | Go server: OAuth sign-in and accounts, rooms and access checks (`internal/access`), friends (`internal/friends`), the WHIP/WHEP media gateway (`internal/gateway`), the realtime WebSocket with presence and chat (`internal/realtime`), and the Discord bot (`internal/discord`). SQLite with goose migrations and sqlc queries. |
| `server/loadtest/` | Load test: synthetic WHIP publishers and WHEP viewers against `make dev` ([README](server/loadtest/README.md)). Never against production. |
| `web/` | Website: React, TypeScript, Vite, Tailwind CSS, TanStack Query, React Router. |
| `companion/` | The desktop app (Rust, GStreamer): for now its media pipeline and the `cupstream-cli` prototype. See [companion/README.md](companion/README.md). |
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

Database changes go in a new migration: `make migration name=add_something` creates `server/internal/store/migrations/<UTC time>_add_something.sql`. Queries go in `server/internal/store/queries/`; run `make generate` afterwards. Migrations are numbered by the time they were created, so branches never compete for the next number, and the server also applies one that merges after a newer one (00001–00010 predate this). In a query, use either `?` or `sqlc.arg(name)` for all parameters, never both: sqlc numbers mixed parameters wrongly. API types live in `server/internal/api/` so the website types can be generated from them.

## Deployment

Every merge into `master` is deployed to https://cupstream.larp.love once CI passes; `/healthz` reports the running commit. [AGENTS.md](AGENTS.md) has the rules for branches, pull requests, deploys and rollbacks.

MediaMTX is installed by hand, as a systemd service next to `cupstream.service`, and only changes when its pinned tag does. Take the binary from the image, so production runs the same build as development and CI:

```sh
docker compose build mediamtx
id=$(docker create cupstream-mediamtx:<tag>) && docker cp $id:/usr/local/bin/mediamtx . && docker rm $id
```

On the server: install it as `/usr/local/bin/mediamtx`, with a system user `mediamtx`, [mediamtx.yml](deploy/mediamtx/mediamtx.yml) as `/etc/mediamtx/mediamtx.yml`, [env.example](deploy/mediamtx/env.example) adapted as `/etc/mediamtx/env` (both `root:mediamtx`, mode 640) and [mediamtx.service](deploy/mediamtx/mediamtx.service) in `/etc/systemd/system/`. Open port 8189 (UDP and TCP) in the firewall, raise the UDP send buffer limit (see [MediaMTX build](docs/mediamtx.md)), then restart `mediamtx` and `cupstream`: the server reads MediaMTX's features at startup, and `/healthz` should report `"pause":true,"events":true`.

## Configuration

The server reads `CUPSTREAM_*` environment variables; see [.env.example](.env.example).

Requests are rate-limited per client address (behind the tunnel, the visitor's address from `CUPSTREAM_CLIENT_IP_HEADER`; for IPv6, per /64): signing in, going live, opening viewer sessions, opening the WebSocket and every other change through the API each have their own limit, and the limits are in `server/internal/httpapi/ratelimit.go`. Media session requests (`/api/media/...`) need the session's secret and are not limited. A refused request gets 429 with `Retry-After` and the code `rate_limited`. MediaMTX's HTTP API and WHIP/WHEP listener are bound to `127.0.0.1`: only the CupStream server talks to them, and media flows on port 8189 (UDP, with TCP fallback).

## Feature notes

- [Signing in with Google and Discord](docs/sign-in.md)
- [Desktop app sign-in](docs/desktop-app.md)
- [Discord bot](docs/discord.md)
- [Realtime](docs/realtime.md)
- [Streaming locally](docs/streaming.md)
- [Capacity](docs/capacity.md)
- [Admins](docs/admin.md)
- [MediaMTX build](docs/mediamtx.md)
