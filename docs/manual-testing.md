# Manual Testing

Use this checklist for the production path that cannot be fully verified by ordinary unit tests.

## Build

```sh
CGO_ENABLED=1 CGO_CFLAGS_ALLOW="-fno-strict-overflow" go build -tags "sherpa pipewire" -trimpath -ldflags "-s -w" -o sway-voice ./cmd/sway-voice
```

Expected: the build succeeds on a machine with `pkg-config`, `libpipewire-0.3` development headers, a C compiler, and sherpa-onnx native libraries available to the dynamic linker.

## Model

```sh
./sway-voice model install parakeet-v3-int8
./sway-voice model check
```

Expected: all required model files pass size/readability checks. If the model was installed manually, set `[asr].model_dir` to the directory containing `encoder.int8.onnx`, `decoder.int8.onnx`, `joiner.int8.onnx`, and `tokens.txt`.

## Diagnostics

```sh
./sway-voice doctor
```

Expected in an active Sway session: `config`, `WAYLAND_DISPLAY`, `SWAYSOCK`, `XDG_RUNTIME_DIR`, `sherpa build`, `PipeWire build`, `wtype`, `PipeWire`, `Sway IPC`, and `model` all report `OK`.

## File Transcription

```sh
SWAY_VOICE_TEST_WAV="$HOME/speech-16khz-mono.wav" make test-model
```

Expected: `transcribe --file` prints non-empty text and `bench` prints JSON with `audio_seconds`, `decode_seconds`, `rtf`, `threads`, `provider`, `model`, and `rss_peak_bytes`.

## Sway Dictation

Add the toggle binding to the Sway config and reload Sway:

```sway
exec_always sway-voice daemon
bindsym --release --no-repeat $mod+v exec sway-voice toggle
bindsym --release --no-repeat $mod+Shift+v exec sway-voice stop --discard
```

Expected: pressing `$mod+v`, speaking one short utterance, and pressing `$mod+v` again inserts recognized text into the focused Wayland text field.

## Focus Safety

Start dictation in one text field, move focus to another window before speech ends, and wait for recognition.

Expected: no text is typed, and `./sway-voice status --json` reports a recent `focus_changed` error. With the default redaction setting, the withheld text is not present in status output.

## PipeWire Lifecycle

```sh
SWAY_VOICE_TEST_PIPEWIRE=1 CGO_ENABLED=1 go test -tags pipewire ./internal/audio/pipewire
```

Expected: capture initializes, starts, reads or times out cleanly, pauses, and stops without crashing.
