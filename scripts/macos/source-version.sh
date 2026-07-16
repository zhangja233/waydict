#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
commit="$(git -C "$root" rev-parse --short=12 HEAD 2>/dev/null || printf unknown)"
if [[ -n "$(git -C "$root" status --porcelain --untracked-files=all 2>/dev/null)" ]]; then
	commit="${commit}-dirty"
fi
printf '%s\n' "$commit"
