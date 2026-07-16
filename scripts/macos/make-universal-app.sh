#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly arm_app="$root/build/macos/arm64/Waydict.app"
readonly x86_app="$root/build/macos/x86_64/Waydict.app"
readonly output="$root/build/Waydict.app"

for app in "$arm_app" "$x86_app"; do
	if [[ ! -d "$app" ]]; then
		echo "missing architecture bundle: $app" >&2
		exit 1
	fi
done
if ! cmp -s "$arm_app/Contents/Info.plist" "$x86_app/Contents/Info.plist"; then
	echo "architecture bundle metadata differs" >&2
	exit 1
fi

rm -rf "$output"
ditto "$arm_app" "$output"

while IFS= read -r -d '' arm_file; do
	relative="${arm_file#"$arm_app/"}"
	x86_file="$x86_app/$relative"
	output_file="$output/$relative"
	if file -b "$arm_file" | grep -q 'Mach-O'; then
		if [[ ! -f "$x86_file" ]] || ! file -b "$x86_file" | grep -q 'Mach-O'; then
			echo "missing x86_64 Mach-O counterpart: $relative" >&2
			exit 1
		fi
		lipo -create "$arm_file" "$x86_file" -output "$output_file"
		lipo -info "$output_file"
	fi
done < <(find "$arm_app/Contents" -type f -print0)

for dylib in "$output/Contents"/Frameworks/*.dylib; do
	codesign -s - --force "$dylib"
done
codesign -s - --force "$output/Contents/MacOS/waydict"
codesign -s - --force --entitlements "$root/packaging/macos/Waydict.entitlements" "$output"
codesign --verify --deep --strict "$output"

while IFS= read -r -d '' file; do
	file -b "$file" | grep -q 'Mach-O' || continue
	archs="$(lipo -archs "$file")"
	if [[ " $archs " != *" arm64 "* || " $archs " != *" x86_64 "* ]]; then
		echo "$file is not universal2: $archs" >&2
		exit 1
	fi
done < <(find "$output/Contents" -type f -print0)
echo "$output"
