#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly version="${1:?usage: $0 VERSION DMG}"
readonly dmg="${2:?usage: $0 VERSION DMG}"
readonly dist="$root/dist"

if [[ ! -f "$dmg" ]]; then
	echo "DMG not found: $dmg" >&2
	exit 1
fi
install -d "$dist"
install -m 0644 "$root/packaging/macos/THIRD_PARTY_NOTICES.md" "$dist/THIRD_PARTY_NOTICES.md"
commit="$(git -C "$root" rev-parse --short=12 HEAD)"
(cd "$root" && go run ./scripts/macos/sbom -version "$version" -commit "$commit" -output "$dist/Waydict-$version.spdx.json")
sha="$(shasum -a 256 "$dmg" | awk '{ print $1 }')"
sed -e "s|@VERSION@|$version|g" -e "s|@SHA256@|$sha|g" "$root/packaging/macos/Casks/waydict.rb.in" > "$dist/waydict.rb"
(
	cd "$dist"
	shasum -a 256 "$(basename "$dmg")" "Waydict-$version.spdx.json" THIRD_PARTY_NOTICES.md waydict.rb > SHA256SUMS
	shasum -a 256 "$(basename "$dmg")" > "$(basename "$dmg").sha256"
)
echo "$dist/SHA256SUMS"
