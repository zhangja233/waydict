#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly arch="${1:?usage: $0 arm64|x86_64 VERSION BUILD_NUMBER COMMIT}"
readonly version="${2:?usage: $0 arm64|x86_64 VERSION BUILD_NUMBER COMMIT}"
readonly build_number="${3:?usage: $0 arm64|x86_64 VERSION BUILD_NUMBER COMMIT}"
readonly commit="${4:?usage: $0 arm64|x86_64 VERSION BUILD_NUMBER COMMIT}"
readonly lock_file="$root/third_party/versions.lock"
readonly deployment_target="$(awk '$1 == "macos-deployment-target" { print $2 }' "$lock_file")"
readonly whisper_commit="$(awk '$1 == "whisper.cpp" { print $2 }' "$lock_file")"
readonly sherpa_version="$(awk '$1 == "sherpa-onnx-go-macos" { print $2 }' "$lock_file")"
readonly onnxruntime_version="$(awk '$1 == "onnxruntime" { print $3 }' "$lock_file")"
readonly catalog_sha="$(shasum -a 256 "$root/packaging/macos/Resources/model-catalog.json" | awk '{ print $1 }')"
readonly xcode_version="$(xcodebuild -version | awk 'NR == 1 { print $2 }')"
readonly sdk_version="$(xcrun --sdk macosx --show-sdk-version)"
readonly tags="coreaudio sherpa whispercpp"
readonly tags_metadata="coreaudio,sherpa,whispercpp"

case "$arch" in
	arm64)
		readonly goarch=arm64
		;;
	x86_64)
		readonly goarch=amd64
		;;
	*)
		echo "unsupported macOS architecture: $arch" >&2
		exit 2
		;;
esac

readonly app="$root/build/macos/$arch/Waydict.app"
readonly contents="$app/Contents"
readonly ldflags="-s -w -X waydict/internal/buildinfo.Version=$version -X waydict/internal/buildinfo.Commit=$commit -X waydict/internal/buildinfo.BuildNumber=$build_number -X waydict/internal/buildinfo.BuildTags=$tags_metadata -X waydict/internal/buildinfo.XcodeVersion=$xcode_version -X waydict/internal/buildinfo.SDKVersion=$sdk_version -X waydict/internal/buildinfo.DeploymentTarget=$deployment_target -X waydict/internal/buildinfo.WhisperCommit=$whisper_commit -X waydict/internal/buildinfo.SherpaVersion=$sherpa_version -X waydict/internal/buildinfo.ONNXRuntimeVersion=$onnxruntime_version -X waydict/internal/buildinfo.ModelCatalogSHA256=$catalog_sha"

"$root/scripts/macos/build-whisper.sh" "$arch"
"$root/scripts/macos/build-onnxruntime.sh" "$arch"

rm -rf "$app"
install -d "$contents/MacOS" "$contents/Resources/en.lproj" "$contents/Frameworks"
common_env=(
	CGO_ENABLED=1
	CGO_CFLAGS_ALLOW=-fno-strict-overflow
	GOOS=darwin
	GOARCH="$goarch"
	MACOSX_DEPLOYMENT_TARGET="$deployment_target"
	CGO_CFLAGS="-arch $arch -mmacosx-version-min=$deployment_target"
	CGO_CXXFLAGS="-arch $arch -mmacosx-version-min=$deployment_target"
	CGO_LDFLAGS="-arch $arch -mmacosx-version-min=$deployment_target"
)
(
	cd "$root"
	env "${common_env[@]}" go build -tags "$tags" -trimpath -ldflags "$ldflags" -o "$contents/MacOS/waydict-app" ./cmd/waydict-app
	env "${common_env[@]}" go build -tags "$tags" -trimpath -ldflags "$ldflags" -o "$contents/MacOS/waydict" ./cmd/waydict
)
"$root/scripts/macos/package-sherpa.sh" "$app" "$arch"

sed -e "s|@VERSION@|$version|g" -e "s|@BUILD_NUMBER@|$build_number|g" "$root/packaging/macos/Info.plist.in" > "$contents/Info.plist"
cp "$root/packaging/macos/Resources/en.lproj/Localizable.strings" "$root/packaging/macos/Resources/en.lproj/InfoPlist.strings" "$contents/Resources/en.lproj/"
cp "$root/packaging/macos/Resources/model-catalog.json" "$contents/Resources/"
cp "$root/packaging/macos/README.txt" "$root/packaging/macos/THIRD_PARTY_NOTICES.md" "$root/LICENSE" "$contents/Resources/"
(cd "$root" && go run ./scripts/macos/icon -output "$contents/Resources/Waydict.icns")
printf 'APPL????' > "$contents/PkgInfo"
plutil -lint "$contents/Info.plist" >/dev/null

for dylib in "$contents"/Frameworks/*.dylib; do
	codesign -s - --force "$dylib"
done
codesign -s - --force "$contents/MacOS/waydict"
codesign -s - --force --entitlements "$root/packaging/macos/Waydict.entitlements" "$app"
codesign --verify --deep --strict "$app"

for binary in "$contents/MacOS/waydict-app" "$contents/MacOS/waydict" "$contents"/Frameworks/*.dylib; do
	if [[ "$(lipo -archs "$binary")" != "$arch" ]]; then
		echo "$binary is $(lipo -archs "$binary"); expected $arch" >&2
		exit 1
	fi
done
echo "$app"
