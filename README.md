# CupStream

Low-latency live streaming for friends: go live from the desktop app, a browser or OBS, and watch together on the website. Rooms are tied to a Discord voice channel or shared through links.

The design and the implementation plan are in [output/md/cupstream-project-overview.md](output/md/cupstream-project-overview.md).

## Layout

| Path | What |
|---|---|
| `server/` | Go server: HTTP API, sessions, and later WHIP/WHEP gateway, realtime and the Discord bot. SQLite with goose migrations and sqlc queries. |
| `web/` | Website: React, TypeScript, Vite, Tailwind CSS, TanStack Query, React Router. |
| `deploy/mediamtx/` | MediaMTX image, built from [our fork](https://github.com/RAdu2005/mediamtx) at a pinned tag, and its configuration. |
| `deploy/cupstream/` | systemd and OpenRC service files. |

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

Database changes go in a new file in `server/internal/store/migrations/`, with queries in `server/internal/store/queries/`; run `make generate` afterwards. API types live in `server/internal/api/` so the website types can be generated from them.

## Configuration

The server reads `CUPSTREAM_*` environment variables; see [.env.example](.env.example). MediaMTX's HTTP API and WHIP/WHEP listener are bound to `127.0.0.1`: only the CupStream server talks to them, and media flows on port 8189 (UDP, with TCP fallback).

## MediaMTX build

MediaMTX runs from the fork [RAdu2005/mediamtx](https://github.com/RAdu2005/mediamtx): an upstream release plus the WHEP pause/resume pull request [bluenviron/mediamtx#6308](https://github.com/bluenviron/mediamtx/pull/6308) and our changes (overview, section 3.2). The tag is pinned in `deploy/mediamtx/Dockerfile` and `compose.yaml`; the fork's `CUPSTREAM.md` describes how to upgrade.
