#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly app="${1:-$root/build/Waydict.app}"
readonly version="${2:-${VERSION:-}}"
readonly output="${3:-$root/dist/Waydict-${version}-universal.dmg}"
readonly staging="$root/build/dmg-root"

if [[ -z "$version" ]]; then
	echo "usage: $0 [APP_BUNDLE] VERSION [OUTPUT]" >&2
	exit 2
fi
if [[ ! -d "$app" ]]; then
	echo "application bundle not found: $app" >&2
	exit 1
fi

rm -rf "$staging"
install -d "$staging" "$(dirname "$output")"
ditto "$app" "$staging/Waydict.app"
ln -s /Applications "$staging/Applications"
install -m 0644 "$root/packaging/macos/README.txt" "$staging/README.txt"
install -m 0644 "$root/LICENSE" "$staging/LICENSE"

# Stable source metadata keeps the image layout independent of checkout times.
find "$staging" -exec touch -h -t 202001010000 {} +
rm -f "$output"
if ! create_output="$(hdiutil create \
		-volname "Waydict $version" \
		-srcfolder "$staging" \
		-fs HFS+ \
		-format UDZO \
		-layout NONE \
		-nospotlight \
		-noanyowners \
		-srcowners off \
		-ov \
		"$output" 2>&1)"; then
	if [[ "${RELEASE:-0}" == 1 ]]; then
		printf '%s\n' "$create_output" >&2
		echo "release DMG creation requires the macOS DiskImages service" >&2
		exit 1
	fi
	printf '%s\n' "$create_output" >&2
	echo "DiskImages service unavailable; using hdiutil HFS hybrid mode" >&2
	hdiutil makehybrid -hfs -hfs-volume-name "Waydict $version" -ov -o "$output" "$staging"
else
	printf '%s\n' "$create_output"
fi
echo "$output"
