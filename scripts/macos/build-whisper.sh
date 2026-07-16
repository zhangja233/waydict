#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly source_dir="$root/third_party/whisper.cpp"
readonly lock_file="$root/third_party/versions.lock"

build_arch() {
	local arch="$1"
	local build_dir="$root/build/whisper-cmake/$arch"
	local prefix="$root/build/whisper/$arch"
	local accelerate_compat="-UACCELERATE_NEW_LAPACK -UACCELERATE_LAPACK_ILP64"

	rm -rf "$build_dir" "$prefix"
	CFLAGS="${CFLAGS:+$CFLAGS }$accelerate_compat" \
		CXXFLAGS="${CXXFLAGS:+$CXXFLAGS }$accelerate_compat" \
		cmake -S "$source_dir" -B "$build_dir" -G Ninja \
		-DCMAKE_INSTALL_PREFIX="$prefix" \
		-DBUILD_SHARED_LIBS=OFF \
		-DWHISPER_BUILD_TESTS=OFF \
		-DWHISPER_BUILD_EXAMPLES=OFF \
		-DWHISPER_BUILD_SERVER=OFF \
		-DWHISPER_CURL=OFF \
		-DWHISPER_COREML=OFF \
		-DGGML_STATIC=ON \
		-DGGML_BACKEND_DL=OFF \
		-DGGML_METAL=ON \
		-DGGML_METAL_EMBED_LIBRARY=ON \
		-DGGML_METAL_MACOSX_VERSION_MIN=13.0 \
		-DGGML_ACCELERATE=ON \
		-DGGML_BLAS=ON \
		-DGGML_BLAS_VENDOR=Apple \
		-DGGML_OPENMP=OFF \
		-DGGML_NATIVE=OFF \
		-DGGML_CCACHE=OFF \
		-DCMAKE_OSX_ARCHITECTURES="$arch" \
		-DCMAKE_OSX_DEPLOYMENT_TARGET=13.0 \
		-DCMAKE_BUILD_TYPE=Release
	cmake --build "$build_dir"
	cmake --install "$build_dir"
	if nm -u "$prefix"/lib/*.a 2>/dev/null | grep -Eq '\$(NEWLAPACK|ILP64)'; then
		echo "Whisper imports an Accelerate ABI newer than macOS 13.0" >&2
		exit 1
	fi
	find "$prefix/lib" -maxdepth 1 -type f -name '*.a' -print | sort
}

if [[ ! -f "$source_dir/CMakeLists.txt" ]]; then
	echo "whisper.cpp submodule is missing; run git submodule update --init" >&2
	exit 1
fi
expected_sha="$(awk '$1 == "whisper.cpp" { print $2 }' "$lock_file")"
actual_sha="$(git -C "$source_dir" rev-parse HEAD)"
if [[ ! "$expected_sha" =~ ^[0-9a-f]{40}$ || "$actual_sha" != "$expected_sha" ]]; then
	echo "whisper.cpp is at $actual_sha; expected $expected_sha" >&2
	exit 1
fi

case "${1:-all}" in
	arm64|x86_64)
		build_arch "$1"
		;;
	all)
		build_arch arm64
		build_arch x86_64
		;;
	*)
		echo "usage: $0 [arm64|x86_64|all]" >&2
		exit 2
		;;
esac
