#!/usr/bin/env bash
# Keeps a clone in step with GitHub after pull requests are merged:
# - fast-forwards the main checkout's master (only when it is on master and
#   clean, so nothing in progress is touched);
# - deletes local branches whose pull request was merged, when the branch is
#   exactly what was merged (no commits added since) and no worktree has it
#   checked out. Branches that were never pushed are left alone.
#
# Run it by hand (`make tidy`), or install a systemd user timer that runs the
# main checkout's copy every 15 minutes: `scripts/tidy.sh --install`.
set -euo pipefail

main=$(dirname "$(git -C "$(dirname "$0")" rev-parse --path-format=absolute --git-common-dir)")

if [ "${1:-}" = --install ]; then
	units=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user
	mkdir -p "$units"
	cat >"$units/cupstream-tidy.service" <<UNIT
[Unit]
Description=Fast-forward the CupStream checkout and delete merged branches

[Service]
Type=oneshot
ExecStart=$main/scripts/tidy.sh
UNIT
	cat >"$units/cupstream-tidy.timer" <<UNIT
[Unit]
Description=Tidy the CupStream checkout every 15 minutes

[Timer]
OnStartupSec=2min
OnUnitActiveSec=15min

[Install]
WantedBy=timers.target
UNIT
	systemctl --user daemon-reload
	systemctl --user enable --now cupstream-tidy.timer
	exit 0
fi
git -C "$main" fetch --prune --quiet origin

if [ "$(git -C "$main" branch --show-current)" = master ] && [ -z "$(git -C "$main" status --porcelain)" ]; then
	git -C "$main" merge --ff-only --quiet origin/master || echo "master has diverged from origin/master; left as is" >&2
fi

checked_out=$(git -C "$main" worktree list --porcelain | sed -n 's|^branch refs/heads/||p')
git -C "$main" for-each-ref --format='%(refname:short) %(objectname) %(upstream:track)' refs/heads |
	while read -r branch tip track; do
		[ "$track" = "[gone]" ] || continue
		grep -qxF "$branch" <<<"$checked_out" && continue
		merged=$(cd "$main" && gh pr list --state merged --head "$branch" --json headRefOid --jq '.[].headRefOid')
		if grep -qxF "$tip" <<<"$merged"; then
			git -C "$main" branch --quiet -D "$branch"
			echo "deleted $branch (merged)"
		fi
	done
