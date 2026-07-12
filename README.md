# waydict

Local voice dictation for wlroots Wayland compositors. A daemon owns the microphone and the speech model; the CLI sends `start`/`stop`/`toggle`/`status` over a per-user Unix socket, and recognized text is typed into the focused window with `wtype`. Recognition runs locally on CPU or a Vulkan GPU; only model installation uses the network.

Pipeline: PipeWire capture → silero VAD → Whisper (whisper.cpp/Vulkan) or Parakeet Unified (sherpa-onnx/CPU) → `wtype`.

## Dependencies

Runtime:

- A wlroots compositor with the virtual-keyboard protocol (sway, river, Hyprland, Wayfire, …) and `wtype`.
- PipeWire.
- At least one ASR model and, preferably, the silero VAD model (downloaded below; not embedded).
- For GPU ASR, a Vulkan-capable GPU with a working ICD and an accessible `/dev/dri/renderD*` node.
- Sway IPC only for the optional focus guard (cancel-on-focus-change); not required elsewhere.

Build: Go with cgo, a C compiler, `pkg-config`, `libpipewire-0.3` headers, and Vulkan-enabled libwhisper. The Nix flake provides these.

## Build

```sh
CGO_ENABLED=1 CGO_CFLAGS_ALLOW=-fno-strict-overflow \
  go build -tags "sherpa pipewire whispercpp" -trimpath -ldflags "-s -w" -o waydict ./cmd/waydict
```

Omit the `whispercpp` tag when building for a CPU-only system without libwhisper. Tests that don't need native ASR libs: `go test ./...`.

## Installation

```sh
install -Dm755 waydict ~/.local/bin/waydict

# Models are not embedded. This installs CPU Parakeet, the default GPU Whisper
# model, and silero VAD. Individual installs are also supported.
waydict model install all

# optional config (sane defaults otherwise)
cp testdata/sample-config.toml ~/.config/waydict.toml

waydict doctor   # verify build, engine resolution, models, Vulkan, and session dependencies
```

Without `--config`, waydict loads `~/.config/waydict.toml`, falling back to `~/.config/waydict/config.toml` (the flat file wins if both exist); `$XDG_CONFIG_HOME` overrides `~/.config`.

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
waydict model   check [--config PATH] [--dir PATH]
waydict model   install <parakeet-unified-en-0.6b-fp32|parakeet-v3-int8|silero-vad|whisper-small-en|whisper-medium-en|whisper-large-v3-turbo|all> [--dir PATH]
waydict bench   --file PATH [--repeat N]
waydict doctor
```

## GPU ASR

The default `[asr] engine = "auto"` prefers `whisper-cpp` with the `ggml-large-v3-turbo` model on Vulkan. If the build, model, Vulkan ICD, or render-node access is missing, it logs the reason, exposes it through status, and falls back to the existing sherpa-onnx CPU path. A forced engine never switches to the other engine, and its resolution or load errors are fatal. CPU-only installations therefore behave as before; see [docs/gpu.md](docs/gpu.md) for setup, model sizes, VRAM use, benchmarks, and troubleshooting.

ASR engine changes require a daemon restart; config reload does not re-resolve the engine.

## Notes

- Garbled output usually means the mic is clipping — lower its gain: `wpctl set-volume @DEFAULT_AUDIO_SOURCE@ 0.6`.
- Tune `[asr] num_threads` with `waydict bench`; target real-time factor ≲ 0.7.
- Privacy: no network except `model install`; transcripts redacted from logs/status; audio not saved; capture paused when idle.
- More help: [docs/troubleshooting.md](docs/troubleshooting.md).

## License

GPL-3.0. See [LICENSE](LICENSE).
