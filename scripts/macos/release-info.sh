#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly lock_file="$root/third_party/versions.lock"
readonly whisper_commit="$(awk '$1 == "whisper.cpp" { print $2 }' "$lock_file")"
readonly ort_commit="$(awk '$1 == "onnxruntime" { print $2 }' "$lock_file")"
readonly ort_version="$(awk '$1 == "onnxruntime" { print $3 }' "$lock_file")"
readonly sherpa_version="$(awk '$1 == "sherpa-onnx-go-macos" { print $2 }' "$lock_file")"
readonly uniseg_version="$(awk '$1 == "uniseg" { print $2 }' "$lock_file")"
readonly deployment_target="$(awk '$1 == "macos-deployment-target" { print $2 }' "$lock_file")"
readonly catalog_sha="$(shasum -a 256 "$root/packaging/macos/Resources/model-catalog.json" | awk '{ print $1 }')"
readonly go_directive="$(awk '$1 == "go" { print $2; exit }' "$root/go.mod")"
readonly xcode_version="$(xcodebuild -version | awk 'NR == 1 { print $2 }')"
readonly sdk_version="$(xcrun --sdk macosx --show-sdk-version)"

cat <<EOF
release inputs: version=${VERSION:-dev} build=${BUILD_NUMBER:-0} commit=$($root/scripts/macos/source-version.sh)
toolchain: go=$(go version | awk '{ print $3 }') (go.mod $go_directive) xcode=$xcode_version sdk=$sdk_version minos=$deployment_target
native pins: whisper=$whisper_commit sherpa=$sherpa_version onnxruntime=$ort_version@$ort_commit uniseg=$uniseg_version
model catalog sha256: $catalog_sha
EOF

[[ "${RELEASE:-0}" == 1 ]] || exit 0

if [[ ! "${VERSION:-}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
	echo "release VERSION must be an explicit semantic version" >&2
	exit 1
fi
if [[ ! "${BUILD_NUMBER:-}" =~ ^[1-9][0-9]*$ ]]; then
	echo "release BUILD_NUMBER must be a positive integer" >&2
	exit 1
fi
if [[ -n "$(git -C "$root" status --porcelain --untracked-files=all)" ]]; then
	echo "release build requires a clean worktree" >&2
	git -C "$root" status --short >&2
	exit 1
fi
if [[ "$(git -C "$root/third_party/whisper.cpp" rev-parse HEAD)" != "$whisper_commit" ]]; then
	echo "whisper.cpp submodule differs from versions.lock" >&2
	exit 1
fi
if [[ "$(cd "$root" && go list -m -f '{{.Version}}' github.com/k2-fsa/sherpa-onnx-go-macos)" != "v$sherpa_version" ]]; then
	echo "sherpa module differs from versions.lock" >&2
	exit 1
fi
if [[ "$(cd "$root" && go list -m -f '{{.Version}}' github.com/rivo/uniseg)" != "v$uniseg_version" ]]; then
	echo "uniseg module differs from versions.lock" >&2
	exit 1
fi
(cd "$root" && go mod verify)
