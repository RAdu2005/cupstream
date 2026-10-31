# Working on CupStream

Rules for coding agents (and a summary for people). The README has the layout, the commands and the configuration.

## Branches and pull requests

- Every task gets its own git worktree and its own branch; T3 Code creates both. Before the first push, rename the branch to `<type>/<short-slug>` (`feat/`, `fix/`, `ops/`, `docs/`, `refactor/`), e.g. `git branch -m fix/room-invite-expiry`.
- One task, one branch, one pull request. Keep unrelated changes out; if you find something else worth doing, mention it rather than folding it in.
- Never commit or push to `master`; a ruleset rejects it anyway. Changes reach `master` only through a pull request whose checks (`server`, `web`, `smoke`) pass.
- Before opening the pull request, run `make test`, and `make check-generated` if you touched queries, migrations or `server/internal/api`. Open it with `gh pr create`; describe what changed, how you checked it, and anything to do by hand on the server.
- **Do not merge pull requests and do not enable auto-merge.** The maintainer merges, and merging deploys.
- The main checkout (the repository's first worktree) stays on `master`, clean. `scripts/tidy.sh` (a systemd user timer, or `make tidy`) fast-forwards it and deletes local branches whose pull request was merged; GitHub deletes the remote branch on merge. Do not commit or switch branches there.
- Tags `v*` publish a GitHub release. Create one only when asked.

## Deployment

- **Merging into `master` is the deploy.** After the checks pass, the `deploy` job in `.github/workflows/ci.yml` builds the binary, installs it on the production server with `deploy/cupstream/cupstream-deploy` and waits until https://cupstream.larp.love/healthz reports the merged commit. If the new server does not come up, the script restores the previous binary and database and the job fails.
- **Never deploy by hand.** No copying binaries to the server, no restarting it to try out a branch: only code merged into `master` runs in production.
- What is live: `curl -s https://cupstream.larp.love/healthz | jq .version`. How the deploy went: `gh run list --branch master --workflow CI`, then `gh run view <id> --log-failed`.
- Roll back by opening a revert pull request (`git revert <commit>`). In an emergency, and only when the maintainer asks, re-run the `deploy` job in the CI run of an earlier commit (`gh run rerun <run-id> --job <job-id>`).
- Only the binary is deployed automatically. Changes on the server itself (the systemd unit, `/etc/cupstream/env`, MediaMTX, the firewall, the tunnel) are made by hand: list them in the pull request. Connect to the server only when the maintainer asks you to, and prefer reading (status, logs) to changing.
- Migrations run when the server starts and are not undone by a rollback. Write them so the previous release still works on the migrated database: add tables and columns; drop or rename them only in a later change, once nothing uses them. Create them with `make migration name=...`. They can be applied in a different order in development and in production (whichever pull request merges first), so a migration must not depend on another branch's unmerged one.

## Public repository

The repository and its CI logs are public. Keep server addresses, hostnames other than `cupstream.larp.love`, secrets, tokens and user data out of commits, pull requests, issues and CI output. Deployment secrets live in the GitHub environment `production`, which only `master` can use.

## Development

- `make dev` runs MediaMTX in Docker, the Go server and Vite on http://localhost:5173, with a development login. A worktree without a `.env` uses the main checkout's.
- To run a second copy from another worktree: `DEV_WEB_PORT=5174 DEV_SERVER_PORT=8081 make dev`. It gets its own MediaMTX container, with ports moved by the same offset (a server closes media sessions it did not create, so copies must not share one). Google and Discord sign-in only work on port 5173.
- Each worktree has its own development database in `data/`.
- Match the surrounding code: its comment density, naming and idiom. Generated files (`*.gen.go`, `web/src/api/types.gen.ts`) come from `make generate`; never edit them by hand.
