#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly lock_file="$root/third_party/versions.lock"
readonly expected_commit="$(awk '$1 == "onnxruntime" { print $2 }' "$lock_file")"
readonly version="$(awk '$1 == "onnxruntime" { print $3 }' "$lock_file")"
readonly deployment_target="$(awk '$1 == "macos-deployment-target" { print $2 }' "$lock_file")"
readonly source_dir="$root/build/onnxruntime-src"

case "${1:-}" in
	arm64|x86_64)
		readonly arch="$1"
		;;
	*)
		echo "usage: $0 arm64|x86_64" >&2
		exit 2
		;;
esac

readonly build_dir="$root/build/onnxruntime-build/$arch"
readonly output="$build_dir/Release/libonnxruntime.$version.dylib"

minos() {
	otool -l "$1" | awk '/cmd LC_BUILD_VERSION/ { found = 1; next } found && /minos / { print $2; exit }'
}

if [[ -f "$output" ]] && lipo -archs "$output" | grep -Fqx "$arch" && [[ "$(minos "$output")" == "$deployment_target" ]]; then
	echo "onnxruntime $version $arch: cached ($expected_commit, minos $deployment_target)"
	exit 0
fi

if [[ ! -d "$source_dir/.git" ]]; then
	mkdir -p "$(dirname "$source_dir")"
	git clone --filter=blob:none --no-checkout https://github.com/microsoft/onnxruntime.git "$source_dir"
	git -C "$source_dir" checkout --detach "$expected_commit"
fi

actual_commit="$(git -C "$source_dir" rev-parse HEAD)"
if [[ "$actual_commit" != "$expected_commit" ]]; then
	echo "onnxruntime source is at $actual_commit; expected $expected_commit" >&2
	exit 1
fi
if [[ -n "$(git -C "$source_dir" status --porcelain --untracked-files=no)" ]]; then
	echo "onnxruntime source has tracked changes; refusing dependency drift" >&2
	exit 1
fi

echo "building onnxruntime $version $arch ($expected_commit, minos $deployment_target)"
(
	cd "$source_dir"
	./build.sh \
		--build_dir "$build_dir" \
		--config Release \
		--update \
		--build \
		--build_shared_lib \
		--parallel "${JOBS:-$(sysctl -n hw.logicalcpu)}" \
		--skip_tests \
		--skip_submodule_sync \
		--skip_pip_install \
		--compile_no_warning_as_error \
		--cmake_generator Ninja \
		--osx_arch "$arch" \
		--apple_deploy_target "$deployment_target" \
		--cmake_extra_defines \
			"CMAKE_OSX_DEPLOYMENT_TARGET=$deployment_target" \
			onnxruntime_BUILD_UNIT_TESTS=OFF \
			onnxruntime_USE_COREML=OFF
)

if [[ ! -f "$output" ]] || ! lipo -archs "$output" | grep -Fqx "$arch" || [[ "$(minos "$output")" != "$deployment_target" ]]; then
	echo "onnxruntime $arch output failed architecture or deployment-target validation" >&2
	exit 1
fi
echo "$output"
