#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly artifact="${1:-${ARTIFACT:-$root/build/Waydict.app}}"
readonly allowed_entitlements="$root/packaging/macos/Waydict.entitlements"
readonly maximum_minos="$(awk '$1 == "macos-deployment-target" { print $2 }' "$root/third_party/versions.lock")"
temp="$(mktemp -d)"
mounted=0

cleanup() {
	if [[ "$mounted" == 1 ]]; then
		hdiutil detach "$temp/mount" -quiet || true
	fi
	rm -rf "$temp"
}
trap cleanup EXIT

case "$artifact" in
	*.dmg)
		mkdir "$temp/mount"
		if attach_output="$(hdiutil attach -readonly -nobrowse -mountpoint "$temp/mount" "$artifact" 2>&1)"; then
			mounted=1
			image_root="$temp/mount"
		else
			if ! command -v 7zz >/dev/null 2>&1; then
				printf '%s\n' "$attach_output" >&2
				echo "cannot inspect DMG: hdiutil attach failed and 7zz is unavailable" >&2
				exit 1
			fi
			mkdir "$temp/extracted"
			7zz x -y -o"$temp/extracted" "$artifact" >/dev/null 2>&1 || true
			image_root="$(find "$temp/extracted" -mindepth 1 -maxdepth 1 -type d -print -quit)"
			if [[ -z "$image_root" || ! -d "$image_root/Waydict.app" ]]; then
				printf '%s\n' "$attach_output" >&2
				echo "fallback extraction could not read the DMG" >&2
				exit 1
			fi
			link_target="$(7zz e -so "$artifact" "$(basename "$image_root")/Applications" 2>/dev/null || true)"
			if [[ "$link_target" != /Applications ]]; then
				echo "fallback extraction could not validate the Applications symlink target" >&2
				exit 1
			fi
			ln -s "$link_target" "$image_root/Applications"
			echo "hdiutil attach unavailable; inspecting extracted HFS image"
		fi
		app="$image_root/Waydict.app"
		actual_root="$(find "$image_root" -mindepth 1 -maxdepth 1 ! -name '.fseventsd' -exec basename {} \; | LC_ALL=C sort)"
		expected_root="$(printf '%s\n' Applications LICENSE README.txt Waydict.app | LC_ALL=C sort)"
		if [[ "$actual_root" != "$expected_root" ]]; then
			echo "DMG root is not the release allowlist:" >&2
			printf '%s\n' "$actual_root" >&2
			exit 1
		fi
		if [[ ! -L "$image_root/Applications" ]] || [[ "$(readlink "$image_root/Applications")" != /Applications ]]; then
			echo "DMG Applications entry is not the /Applications symlink" >&2
			exit 1
		fi
		codesign --verify --verbose=1 "$artifact"
		;;
	*.app)
		app="$artifact"
		;;
	*)
		echo "ARTIFACT must be a .app or .dmg: $artifact" >&2
		exit 2
		;;
esac

if [[ ! -d "$app" ]]; then
	echo "Waydict.app is missing from $artifact" >&2
	exit 1
fi

dependencies() {
	otool -L "$1" | awk 'substr($0, 1, 1) == "\t" { print $1 }'
}

rpaths() {
	otool -l "$1" | awk '/cmd LC_RPATH/ { found = 1; next } found && /path / { print $2; found = 0 }'
}

min_versions() {
	otool -l "$1" | awk '/cmd LC_BUILD_VERSION/ { build = 1; next } build && /minos / { print $2; build = 0 } /cmd LC_VERSION_MIN_MACOSX/ { legacy = 1; next } legacy && /version / { print $2; legacy = 0 }'
}

echo "verifying universal Mach-O files:"
while IFS= read -r -d '' binary; do
	file -b "$binary" | grep -q 'Mach-O' || continue
	archs="$(lipo -archs "$binary")"
	if [[ " $archs " != *" arm64 "* || " $archs " != *" x86_64 "* ]]; then
		echo "$binary lacks a universal2 slice: $archs" >&2
		exit 1
	fi
	echo "  ${binary#"$app/"}: $archs"
	while IFS= read -r dep; do
		case "$dep" in
			/System/Library/*|/usr/lib/*|@rpath/*|@loader_path/*|@executable_path/*) ;;
			/*) echo "$binary has absolute non-system dependency $dep" >&2; exit 1 ;;
		esac
	done < <(dependencies "$binary")
	while IFS= read -r path; do
		case "$path" in
			/System/Library/*|/usr/lib/*|@rpath/*|@loader_path/*|@executable_path/*) ;;
			/*) echo "$binary has absolute non-system rpath $path" >&2; exit 1 ;;
		esac
	done < <(rpaths "$binary")
	while IFS= read -r minos; do
		if [[ "$minos" != "$maximum_minos" ]]; then
			echo "$binary targets macOS $minos; expected $maximum_minos" >&2
			exit 1
		fi
	done < <(min_versions "$binary")
done < <(find "$app/Contents" -type f -print0)

main="$app/Contents/MacOS/waydict-app"
cli="$app/Contents/MacOS/waydict"
main_minos="$(min_versions "$main" | sort -u)"
if [[ "$main_minos" != "$maximum_minos" ]]; then
	echo "main executable minos is $main_minos; expected $maximum_minos" >&2
	exit 1
fi
echo "deployment target: $maximum_minos (all bundled slices)"

codesign --verify --deep --strict --verbose=2 "$app"
codesign -d --entitlements :- "$main" > "$temp/main-entitlements.plist" 2>/dev/null
cp "$allowed_entitlements" "$temp/allowed-entitlements.plist"
plutil -convert xml1 "$temp/main-entitlements.plist" "$temp/allowed-entitlements.plist"
if ! cmp -s "$temp/main-entitlements.plist" "$temp/allowed-entitlements.plist"; then
	echo "main app entitlements differ from the release allowlist" >&2
	exit 1
fi
if codesign -d --entitlements :- "$cli" > "$temp/cli-entitlements.plist" 2>/dev/null && [[ -s "$temp/cli-entitlements.plist" ]]; then
	echo "inner CLI unexpectedly has entitlements" >&2
	exit 1
fi
echo "entitlements: com.apple.security.device.audio-input only"

plist_version="$(plutil -extract CFBundleShortVersionString raw "$app/Contents/Info.plist")"
plist_build="$(plutil -extract CFBundleVersion raw "$app/Contents/Info.plist")"
"$cli" version --json > "$temp/version.json"
binary_version="$(plutil -extract version raw "$temp/version.json")"
binary_build="$(plutil -extract build_number raw "$temp/version.json")"
if [[ "$plist_version" != "$binary_version" || "$plist_build" != "$binary_build" ]]; then
	echo "Info.plist version $plist_version/$plist_build differs from binary $binary_version/$binary_build" >&2
	exit 1
fi
echo "version metadata: $plist_version ($plist_build)"

set +e
spctl_output="$(spctl --assess --type execute --verbose=2 "$app" 2>&1)"
spctl_status=$?
set -e
if [[ $spctl_status -eq 0 ]]; then
	echo "Gatekeeper: accepted"
else
	echo "Gatekeeper: rejected/unnotarized (expected for an ad-hoc artifact): ${spctl_output//$'\n'/; }"
fi
echo "structural verification passed: $artifact"
