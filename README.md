# waydict

Local voice dictation for wlroots Wayland compositors. A daemon owns the microphone and the speech model; the CLI sends recording and commit commands over a per-user Unix socket, and recognized text is typed into the focused window with `wtype`. Recognition runs locally on CPU or a Vulkan GPU; only model installation uses the network.

Pipeline: PipeWire capture → silero VAD → Whisper (whisper.cpp/Vulkan) or Parakeet Unified (sherpa-onnx/CPU) → post-processing → `wtype`.

## Dependencies

Runtime:

- A wlroots compositor with the virtual-keyboard protocol (sway, river, Hyprland, Wayfire, …) and `wtype`.
- PipeWire.
- At least one ASR model and, preferably, the silero VAD model (downloaded below; not embedded).
- For GPU ASR, a Vulkan-capable GPU with a working ICD and an accessible `/dev/dri/renderD*` node.
- Sway IPC only for the optional focus guard (cancel-on-focus-change); not required elsewhere.

Build: Go with cgo, a C compiler, `pkg-config`, and `libpipewire-0.3` headers. The `whispercpp` build tag additionally requires Vulkan-enabled libwhisper and the Vulkan loader. The Nix flake provides these.

## Build

```sh
CGO_ENABLED=1 CGO_CFLAGS_ALLOW=-fno-strict-overflow \
  go build -tags "sherpa pipewire whispercpp" -trimpath -ldflags "-s -w" -o waydict ./cmd/waydict
```

Omit the `whispercpp` tag when building for a CPU-only system without libwhisper. Tests that don't need native ASR libs: `go test ./...`.

The default Nix package includes both engines. Build the sherpa-only variant without the Whisper or Vulkan dependencies for CPU-only systems:

```sh
nix build .#sherpa
nix develop .#sherpa
```

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
bindsym --no-repeat F1 exec waydict start --mode hold
bindsym --release --no-repeat F1 exec waydict release
bindsym --release --no-repeat $mod+a exec waydict toggle
```

Hold and toggle modes decode completed VAD segments in the background but buffer their text until explicit finalization. `release` only finalizes a hold session, so a stray key-up cannot close a toggle session. Oneshot mode retains automatic injection after its first segment. On non-Sway wlroots compositors set `[sway] require_sway=false` and `focus_check=false` (you lose the focus guard) and bind the same commands.

Commands:

```text
waydict daemon [--config PATH] [--foreground] [--log-level LEVEL]
waydict start  [--mode toggle|oneshot|hold]
waydict stop   [--commit|--discard]
waydict release
waydict toggle
waydict status [--json]
waydict transcribe --file PATH [--inject]
waydict model   check [--config PATH] [--dir PATH]
waydict model   install <parakeet-unified-en-0.6b-fp32|parakeet-v3-int8|silero-vad|whisper-model-name|all> [--dir PATH]
waydict bench   --file PATH [--repeat N]
waydict doctor
```

### Dictation text

```toml
[postprocess]
smart_case = true
```

`smart_case` defaults to `true`. It capitalizes a segment's first word only when the segment completes a sentence at a sentence boundary; dictated words, phrases, and fragments stay lowercase. Standalone `I`, its contractions, and all-caps acronyms keep their casing across segments.

Reloading config applies `[postprocess]` changes, including `smart_case`. See [docs/sway.md](docs/sway.md) for the segment behavior.

## GPU ASR

The default `[asr] engine = "auto"` prefers `whisper-cpp` with the `ggml-large-v3-turbo` model on Vulkan. If the build, model, Vulkan ICD, or render-node access is missing, it logs the reason, exposes it through status, and falls back to the existing sherpa-onnx CPU path. A forced engine never switches to the other engine, and its resolution or load errors are fatal. CPU-only installations therefore behave as before; see [docs/gpu.md](docs/gpu.md) for setup, model sizes, VRAM use, benchmarks, and troubleshooting.

`[asr].whisper_model` accepts any bare whisper.cpp ggml model name, and the same name installs it: `waydict model install ggml-large-v3-turbo`. waydict owns the model path at `${XDG_DATA_HOME:-~/.local/share}/waydict/models/whisper/<name>.bin`; there is no model-path config for Whisper. The existing install-only `--dir` override is available for staging elsewhere. The catalog names `ggml-small.en`, `ggml-medium.en`, and `ggml-large-v3-turbo` have pinned sizes and SHA-256 checksums. Other names are fetched from the whisper.cpp model repository, require a plausible size, and emit an unpinned-integrity warning.

ASR engine changes require a daemon restart; config reload does not re-resolve the engine.

## Remote ASR

`[asr] engine = "remote"` decodes on another host's daemon instead of locally, so a CPU-only laptop can borrow a desktop's GPU. Capture, VAD, post-processing, and injection all stay local — only the decode crosses. See [docs/remote.md](docs/remote.md) for the full setup.

waydict does not open a network socket for this. It dials a Unix socket at `[asr.remote] socket` and leaves reachability, authentication, and encryption to whatever points that socket at the peer — in practice an SSH forward:

```sh
peer_runtime=$(ssh "$peer" 'echo "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"')
ssh -N -o ExitOnForwardFailure=yes \
  -L "$XDG_RUNTIME_DIR/waydict/asr-remote.sock:$peer_runtime/waydict/waydict.sock" "$peer"
```

The serving host opts in with `[daemon] serve_remote_asr = true`; it then answers `transcribe` with its already-loaded engine, adding no second model to VRAM. The request carries 16-bit PCM (a 5s clip is ~160 KB) and the reply carries text; the serving host neither logs the transcript nor includes its own status in the reply.

When the peer is unreachable — roaming, desktop asleep, tunnel down — `[asr.remote] fallback = "sherpa-onnx"` decodes locally instead, using the same `[asr]` model keys. A refused dial fails in microseconds, and the remote attempt only spends half the segment's deadline, so the fallback always has time to finish. Set `fallback = "none"` to fail loudly instead. `waydict status --json` reports `asr.remote.served` as `remote` or `fallback` for the last segment, and `waydict doctor` probes the peer.

## Notes

- Garbled output usually means the mic is clipping — lower its gain: `wpctl set-volume @DEFAULT_AUDIO_SOURCE@ 0.6`.
- Tune `[asr] num_threads` with `waydict bench`; target real-time factor ≲ 0.7.
- Privacy: no network except `model install`; transcripts redacted from logs/status; audio not saved; capture paused when idle. `engine = "remote"` still opens no network socket — it dials a Unix socket you point at a peer.
- More help: [docs/troubleshooting.md](docs/troubleshooting.md).

## License

GPL-3.0. See [LICENSE](LICENSE).
