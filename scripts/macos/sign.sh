#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly app="${1:-$root/build/Waydict.app}"
readonly identity="${2:-${DEVELOPER_ID_APPLICATION:-}}"

if [[ -z "$identity" ]]; then
	echo "DEVELOPER_ID_APPLICATION is required" >&2
	exit 2
fi
if [[ ! -d "$app" ]]; then
	echo "application bundle not found: $app" >&2
	exit 1
fi
if ! security find-identity -v -p codesigning | grep -Fq "\"$identity\""; then
	echo "Developer ID signing identity is unavailable: $identity" >&2
	echo "Import the certificate and private key into the active keychain before signing." >&2
	exit 1
fi

readonly contents="$app/Contents"
readonly main="$contents/MacOS/waydict-app"
readonly cli="$contents/MacOS/waydict"

# Sign leaf code first so every enclosing seal records its final signature.
while IFS= read -r -d '' file; do
	[[ "$file" == "$main" || "$file" == "$cli" ]] && continue
	file -b "$file" | grep -q 'Mach-O' || continue
	codesign --force --sign "$identity" --options runtime --timestamp "$file"
done < <(find "$contents" -type f -print0)

while IFS= read -r -d '' bundle; do
	codesign --force --sign "$identity" --options runtime --timestamp "$bundle"
done < <(find "$contents" -depth -type d \( -name '*.framework' -o -name '*.xpc' -o -name '*.appex' -o -name '*.app' \) -print0)

codesign --force --sign "$identity" --identifier io.github.zhangja233.waydict.cli --options runtime --timestamp "$cli"
codesign --force --sign "$identity" --options runtime --timestamp --entitlements "$root/packaging/macos/Waydict.entitlements" "$app"
codesign --verify --deep --strict --verbose=2 "$app"
echo "signed $app with $identity"
