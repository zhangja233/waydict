#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly app="${1:?usage: $0 APP_BUNDLE ARCH}"
readonly arch="${2:?usage: $0 APP_BUNDLE ARCH}"
readonly frameworks="$app/Contents/Frameworks"

case "$arch" in
	arm64)
		readonly module_arch="aarch64-apple-darwin"
		;;
	x86_64|amd64)
		readonly module_arch="x86_64-apple-darwin"
		;;
	*)
		echo "unsupported sherpa architecture: $arch" >&2
		exit 2
		;;
esac

readonly module_dir="${SHERPA_ONNX_MACOS_DIR:-$(cd "$root" && go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-macos)}"
readonly source_lib="$module_dir/lib/$module_arch"

install -d "$frameworks"
install -m 0644 "$source_lib/libsherpa-onnx-c-api.dylib" "$frameworks/"
install -m 0644 "$source_lib/libonnxruntime.1.24.4.dylib" "$frameworks/"

dependencies() {
	otool -L "$1" | awk 'NR > 1 { print $1 }'
}

rpaths() {
	otool -l "$1" | awk '/cmd LC_RPATH/ { found = 1; next } found && /path / { print $2; found = 0 }'
}

rewrite_dependencies() {
	local file="$1"
	local dep base
	while IFS= read -r dep; do
		case "$dep" in
		/System/Library/*|/usr/lib/*|@rpath/*|@loader_path/*|@executable_path/*)
			;;
		/*)
			base="$(basename "$dep")"
			if [[ ! -f "$frameworks/$base" ]]; then
				echo "$file has unbundled dependency $dep" >&2
				exit 1
			fi
			install_name_tool -change "$dep" "@rpath/$base" "$file"
			;;
		esac
	done < <(dependencies "$file")
}

verify_dependencies() {
	local file="$1"
	local dep
	while IFS= read -r dep; do
		case "$dep" in
		/System/Library/*|/usr/lib/*|@rpath/*|@loader_path/*|@executable_path/*)
			;;
		/*)
			echo "$file retains absolute non-system dependency $dep" >&2
			exit 1
			;;
		esac
	done < <(dependencies "$file")
}

for dylib in "$frameworks"/*.dylib; do
	install_name_tool -id "@rpath/$(basename "$dylib")" "$dylib"
	rewrite_dependencies "$dylib"
	verify_dependencies "$dylib"
done

for executable in "$app/Contents/MacOS/waydict-app" "$app/Contents/MacOS/waydict"; do
	while IFS= read -r path; do
		case "$path" in
		@executable_path/../Frameworks)
			;;
		/System/Library/*|/usr/lib/*)
			;;
		/*)
			install_name_tool -delete_rpath "$path" "$executable"
			;;
		esac
	done < <(rpaths "$executable")
	if ! rpaths "$executable" | grep -Fqx '@executable_path/../Frameworks'; then
		install_name_tool -add_rpath '@executable_path/../Frameworks' "$executable"
	fi
	rewrite_dependencies "$executable"
	verify_dependencies "$executable"
	while IFS= read -r path; do
		case "$path" in
		/System/Library/*|/usr/lib/*|@executable_path/*|@loader_path/*|@rpath/*)
			;;
		/*)
			echo "$executable retains absolute non-system rpath $path" >&2
			exit 1
			;;
		esac
	done < <(rpaths "$executable")
done

find "$frameworks" -maxdepth 1 -type f -name '*.dylib' -print | sort
