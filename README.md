# waydict

Local voice dictation for wlroots Wayland compositors. A daemon owns the microphone and the speech model; the CLI sends `start`/`stop`/`toggle`/`status` over a per-user Unix socket, and recognized text is typed into the focused window with `wtype`. Everything runs locally on CPU — no network.

Pipeline: PipeWire capture → silero VAD → Parakeet-TDT ASR (sherpa-onnx) → `wtype`.

## Dependencies

Runtime:
- A wlroots compositor with the virtual-keyboard protocol (sway, river, Hyprland, Wayfire, …) and `wtype`.
- PipeWire.
- The Parakeet ASR and silero VAD models (downloaded below; not embedded).
- Sway IPC only for the optional focus guard (cancel-on-focus-change); not required elsewhere.

Build: Go with cgo, a C compiler, `pkg-config`, and `libpipewire-0.3` headers. (`nix-shell` provides these.)

## Build

```sh
CGO_ENABLED=1 CGO_CFLAGS_ALLOW=-fno-strict-overflow \
  go build -tags "sherpa pipewire" -trimpath -ldflags "-s -w" -o waydict ./cmd/waydict
```

Tests that don't need native ASR libs: `go test ./...`.

## Installation

```sh
install -Dm755 waydict ~/.local/bin/waydict

# models (not embedded)
waydict model install parakeet-v3-int8
curl -fL -o ~/.local/share/waydict/models/silero_vad.onnx \
  https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx

# optional config (sane defaults otherwise)
mkdir -p ~/.config/waydict
cp testdata/sample-config.toml ~/.config/waydict/config.toml

waydict doctor   # verify build, models, wtype, PipeWire, compositor
```

## Usage

Start the daemon and bind keys. Sway example:

```sway
exec_always waydict daemon
bindsym --release --no-repeat F2 exec waydict start --mode toggle
bindsym --release --no-repeat F3 exec waydict stop --commit
```

`waydict toggle` is a single-key alternative; hold-to-talk uses `--mode hold`. On non-Sway wlroots compositors set `[sway] require_sway=false` and `focus_check=false` (you lose the focus guard) and bind the same commands.

Commands:

```text
waydict daemon [--config PATH] [--foreground] [--log-level LEVEL]
waydict start  [--mode toggle|oneshot|hold]
waydict stop   [--commit|--discard]
waydict toggle
waydict status [--json]
waydict transcribe --file PATH [--inject]
waydict model   check|install parakeet-v3-int8 [--dir PATH]
waydict bench   --file PATH [--repeat N]
waydict doctor
```

## Notes

- Garbled output usually means the mic is clipping — lower its gain: `wpctl set-volume @DEFAULT_AUDIO_SOURCE@ 0.6`.
- Tune `[asr] num_threads` with `waydict bench`; target real-time factor ≲ 0.7.
- Privacy: no network except `model install`; transcripts redacted from logs/status; audio not saved; capture paused when idle.
- More help: [docs/troubleshooting.md](docs/troubleshooting.md).

## License

GPL-3.0. See [LICENSE](LICENSE).
