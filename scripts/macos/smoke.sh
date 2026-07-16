#!/usr/bin/env bash
set -euo pipefail

readonly root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly artifact="${1:-${ARTIFACT:-$root/build/Waydict.app}}"
temp="$(mktemp -d)"
mounted=0
launcher_pid=""

cleanup() {
	if [[ -n "$launcher_pid" ]] && kill -0 "$launcher_pid" 2>/dev/null; then
		kill "$launcher_pid" 2>/dev/null || true
		wait "$launcher_pid" 2>/dev/null || true
	fi
	if [[ "$mounted" == 1 ]]; then
		hdiutil detach "$temp/mount" -quiet || true
	fi
	rm -rf "$temp"
}
trap cleanup EXIT

case "$artifact" in
	*.dmg)
		mkdir "$temp/mount"
		if hdiutil attach -readonly -nobrowse -mountpoint "$temp/mount" "$artifact" >/dev/null 2>&1; then
			mounted=1
			source_app="$temp/mount/Waydict.app"
		elif command -v 7zz >/dev/null 2>&1; then
			mkdir "$temp/extracted"
			7zz x -y -o"$temp/extracted" "$artifact" >/dev/null 2>&1 || true
			source_app="$(find "$temp/extracted" -type d -name Waydict.app -print -quit)"
			if [[ -z "$source_app" ]]; then
				echo "could not extract Waydict.app from the DMG" >&2
				exit 1
			fi
			echo "hdiutil attach unavailable; smoking the extracted HFS image"
		else
			echo "cannot inspect DMG: hdiutil attach failed and 7zz is unavailable" >&2
			exit 1
		fi
		;;
	*.app) source_app="$artifact" ;;
	*) echo "ARTIFACT must be a .app or .dmg" >&2; exit 2 ;;
esac

app="$temp/Waydict.app"
ditto "$source_app" "$app"
socket="$temp/control.sock"
config="$temp/config.toml"
cat > "$config" <<EOF
[daemon]
socket = "$socket"
preload_model = false

[vad]
engine = "energy"

[focus]
enabled = false
backend = "none"

[hotkey]
enabled = false
EOF

mkdir -p "$temp/home"
/usr/bin/open -n -W \
	--env "WAYDICT_CONFIG=$config" \
	--env WAYDICT_TEST_SUPPRESS_ONBOARDING=1 \
	--env "HOME=$temp/home" \
	--env "CFFIXED_USER_HOME=$temp/home" \
	"$app" >"$temp/app.log" 2>&1 &
launcher_pid=$!
for _ in $(seq 1 100); do
	if HOME="$temp/home" WAYDICT_CONFIG="$config" "$app/Contents/MacOS/waydict" --no-launch status --json > "$temp/status.json" 2>/dev/null; then
		break
	fi
	if ! kill -0 "$launcher_pid" 2>/dev/null; then
		echo "app exited before opening its control socket" >&2
		cat "$temp/app.log" >&2
		exit 1
	fi
	sleep 0.1
done
if [[ ! -s "$temp/status.json" ]]; then
	echo "app did not answer status over its control socket" >&2
	cat "$temp/app.log" >&2
	exit 1
fi
HOME="$temp/home" WAYDICT_CONFIG="$config" "$app/Contents/MacOS/waydict" --no-launch shutdown >/dev/null
for _ in $(seq 1 50); do
	kill -0 "$launcher_pid" 2>/dev/null || break
	sleep 0.1
done
if kill -0 "$launcher_pid" 2>/dev/null; then
	echo "app did not terminate after shutdown" >&2
	exit 1
fi
wait "$launcher_pid"
launcher_pid=""
echo "smoke passed: bundle launch, control socket, status, shutdown"
