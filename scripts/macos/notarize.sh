#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly app="${1:-$root/build/Waydict.app}"
readonly profile="${2:-${NOTARY_PROFILE:-}}"
readonly version="${3:-${VERSION:-}}"
readonly dist="$root/dist"
readonly archive="$dist/Waydict-$version-notary.zip"
readonly dmg="$dist/Waydict-$version-universal.dmg"
temp="$(mktemp -d)"
trap 'rm -rf "$temp"' EXIT

if [[ -z "$profile" ]]; then
	echo "NOTARY_PROFILE is required" >&2
	exit 2
fi
if [[ -z "$version" ]]; then
	echo "VERSION is required" >&2
	exit 2
fi
if [[ ! -d "$app" ]]; then
	echo "signed application bundle not found: $app" >&2
	exit 1
fi
if ! xcrun notarytool history --keychain-profile "$profile" --output-format json >/dev/null 2>&1; then
	echo "notarytool profile is unavailable or rejected: $profile" >&2
	echo "Create it with: xcrun notarytool store-credentials $profile" >&2
	exit 1
fi

identity="$(codesign -dvv "$app" 2>&1 | awk -F= '$1 == "Authority" && $2 ~ /^Developer ID Application:/ { print $2; exit }')"
if [[ -z "$identity" ]]; then
	echo "the app is not signed with a Developer ID Application identity" >&2
	exit 1
fi

install -d "$dist"
rm -f "$archive"
ditto -c -k --keepParent "$app" "$archive"
xcrun notarytool submit "$archive" --keychain-profile "$profile" --wait --output-format json | tee "$dist/notary-app.json"
xcrun stapler staple "$app"
xcrun stapler validate "$app"

"$root/scripts/macos/make-dmg.sh" "$app" "$version" "$dmg"
codesign --force --sign "$identity" --timestamp "$dmg"
xcrun notarytool submit "$dmg" --keychain-profile "$profile" --wait --output-format json | tee "$dist/notary-dmg.json"
xcrun stapler staple "$dmg"
xcrun stapler validate "$dmg"
spctl --assess --type execute --verbose=2 "$app"
spctl --assess --type open --context context:primary-signature --verbose=2 "$dmg"

quarantine_copy="$temp/Waydict.app"
ditto "$app" "$quarantine_copy"
xattr -w com.apple.quarantine '0081;00000000;WaydictRelease;' "$quarantine_copy"
spctl --assess --type execute --verbose=2 "$quarantine_copy"
echo "$dmg"
