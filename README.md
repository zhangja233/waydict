# waydict

Local voice dictation for wlroots Wayland compositors. A daemon owns the microphone and the speech model; the CLI sends `start`/`stop`/`toggle`/`status` over a per-user Unix socket, and recognized text is typed into the focused window with `wtype`. Recognition runs locally on CPU or a Vulkan GPU; only model installation uses the network.

Pipeline: PipeWire capture → silero VAD → Whisper (whisper.cpp/Vulkan) or Parakeet Unified (sherpa-onnx/CPU) → post-processing → `wtype`.

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
waydict model   install <parakeet-unified-en-0.6b-fp32|parakeet-v3-int8|silero-vad|whisper-model-name|all> [--dir PATH]
waydict bench   --file PATH [--repeat N]
waydict doctor
```

### Dictation text

```toml
[asr]
vocabulary = ["Claude", "Codex"]

[postprocess]
smart_case = true

[postprocess.replacements]
cloud = "Claude"
codec = "Codex"
```

`smart_case` defaults to `true`. It capitalizes a segment's first word only when the segment completes a sentence at a sentence boundary; dictated words, phrases, and fragments stay lowercase. Standalone `I` and its contractions, all-caps acronyms, and terms in `asr.vocabulary` keep their casing across segments.

Replacements default to an empty table and are deterministic, case-insensitive whole-word substitutions on every engine. They are deliberately blunt: with the example above, a genuine "cloud" also becomes "Claude". The target is inserted with its configured casing, which is preserved even when the word begins a fragment (the target's first word is protected from smart-case lowercasing).

`asr.vocabulary` defaults to an empty list. On Whisper it biases decoding through the initial prompt; sherpa-onnx/Parakeet uses it only as the smart-case protect-list and relies on replacements for corrections. Keep the list focused on terms you actually dictate: strong Whisper bias can emit a primed term for very short or ambiguous audio.

Reloading config applies `[postprocess]` changes, including `smart_case` and replacements. Changing `asr.vocabulary`, like any other `[asr]` setting, requires a daemon restart. See [docs/sway.md](docs/sway.md) for the segment and host-specific behavior.

## GPU ASR

The default `[asr] engine = "auto"` prefers `whisper-cpp` with the `ggml-large-v3-turbo` model on Vulkan. If the build, model, Vulkan ICD, or render-node access is missing, it logs the reason, exposes it through status, and falls back to the existing sherpa-onnx CPU path. A forced engine never switches to the other engine, and its resolution or load errors are fatal. CPU-only installations therefore behave as before; see [docs/gpu.md](docs/gpu.md) for setup, model sizes, VRAM use, benchmarks, and troubleshooting.

`[asr].whisper_model` accepts any bare whisper.cpp ggml model name, and the same name installs it: `waydict model install ggml-large-v3-turbo`. waydict owns the model path at `${XDG_DATA_HOME:-~/.local/share}/waydict/models/whisper/<name>.bin`; there is no model-path config for Whisper. The existing install-only `--dir` override is available for staging elsewhere. The catalog names `ggml-small.en`, `ggml-medium.en`, and `ggml-large-v3-turbo` have pinned sizes and SHA-256 checksums. Other names are fetched from the whisper.cpp model repository, require a plausible size, and emit an unpinned-integrity warning.

ASR engine changes require a daemon restart; config reload does not re-resolve the engine.

## Notes

- Garbled output usually means the mic is clipping — lower its gain: `wpctl set-volume @DEFAULT_AUDIO_SOURCE@ 0.6`.
- Tune `[asr] num_threads` with `waydict bench`; target real-time factor ≲ 0.7.
- Privacy: no network except `model install`; transcripts redacted from logs/status; audio not saved; capture paused when idle.
- More help: [docs/troubleshooting.md](docs/troubleshooting.md).

## License

GPL-3.0. See [LICENSE](LICENSE).
