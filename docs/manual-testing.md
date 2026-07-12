# Manual Testing

Use this checklist for the production path that cannot be fully verified by ordinary unit tests.

## Build

```sh
CGO_ENABLED=1 CGO_CFLAGS_ALLOW="-fno-strict-overflow" go build -tags "sherpa pipewire whispercpp" -trimpath -ldflags "-s -w" -o waydict ./cmd/waydict
```

Expected: the build succeeds on a machine with `pkg-config`, `libpipewire-0.3` development headers, a C compiler, sherpa-onnx native libraries, and Vulkan-enabled libwhisper available to the dynamic linker. The flake provides these dependencies.

## Model

```sh
./waydict model install parakeet-unified-en-0.6b-fp32
./waydict model install ggml-large-v3-turbo
./waydict model check
```

Expected: the JSON or text result identifies the configured engine and each usable engine/model pair. If the Parakeet model was installed manually, set `[asr].model_dir` to the directory containing `encoder.onnx`, `encoder.weights`, `decoder.onnx`, `joiner.onnx`, and `tokens.txt`.

## Diagnostics

```sh
./waydict doctor
```

Expected in an active Sway session: `config`, `WAYLAND_DISPLAY`, `SWAYSOCK`, `XDG_RUNTIME_DIR`, `PipeWire build`, `wtype`, `PipeWire`, `Sway IPC`, and `model` report `OK`. Check `asr configured`, then require `asr resolution` to report `engine=whisper-cpp provider=vulkan` on the GPU path. `Vulkan ICD` should name the discovered directory.

## Engine Resolution

Prepare one config with forced GPU Whisper:

```toml
[asr]
engine = "whisper-cpp"
provider = "vulkan"
whisper_model = "ggml-large-v3-turbo"
gpu_device = 0
```

Run file transcription with that config, then repeat with `engine = "sherpa-onnx"` and `provider = "cpu"` in a second config:

```sh
./waydict transcribe --config /path/to/whisper.toml --file "$WAYDICT_TEST_WAV"
./waydict transcribe --config /path/to/sherpa.toml --file "$WAYDICT_TEST_WAV"
```

Expected: both print non-empty text; forced Whisper never falls back to sherpa-onnx, and a missing GPU/model or load failure is fatal. With a daemon, also require status to confirm `resolved_provider` is `vulkan`, since the native library can report a post-load CPU backend downgrade. Finally set `engine = "auto"`, run `./waydict doctor --config /path/to/auto.toml`, and verify the `asr resolution` line selects Vulkan Whisper or gives the exact sherpa-onnx fallback reason. Restart the daemon after changing `[asr]`; reload does not re-resolve it.

## File Transcription

```sh
WAYDICT_TEST_WAV="$HOME/speech-16khz-mono.wav" make test-model
```

Expected: `transcribe --file` prints non-empty text and `bench` prints JSON with `audio_seconds`, `decode_seconds`, `rtf`, `threads`, `engine`, `provider`, `model`, and `rss_peak_bytes`.

## Sway Dictation

Add the toggle binding to the Sway config and reload Sway:

```sway
exec_always waydict daemon
bindsym --release --no-repeat $mod+v exec waydict toggle
bindsym --release --no-repeat $mod+Shift+v exec waydict stop --discard
```

Expected: pressing `$mod+v`, speaking one short utterance, and pressing `$mod+v` again inserts recognized text into the focused Wayland text field.

## Focus Safety

Start dictation in one text field, move focus to another window before speech ends, and wait for recognition.

Expected: no text is typed, and `./waydict status --json` reports a recent `focus_changed` error. With the default redaction setting, the withheld text is not present in status output.

## PipeWire Lifecycle

```sh
WAYDICT_TEST_PIPEWIRE=1 CGO_ENABLED=1 go test -tags pipewire ./internal/audio/pipewire
```

Expected: capture initializes, starts, reads or times out cleanly, pauses, and stops without crashing.
