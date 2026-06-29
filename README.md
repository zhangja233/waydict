# Sway Voice Input

`sway-voice` is a local dictation tool for Sway on Wayland. A long-running daemon owns microphone capture and the ASR model, while the CLI sends `toggle`, `start`, `stop`, and `status` commands over a per-user Unix socket. Recognized text is typed into the focused Wayland client with `wtype`.

## Supported Environment

Supported: Linux, Sway, Wayland, PipeWire, `wtype`, Go with cgo enabled, and the sherpa-onnx Parakeet-TDT-0.6B-v3 INT8 model on CPU.

Not supported in v1: portals, Flatpak/Snap/AppImage packaging, PulseAudio/ALSA/JACK/PortAudio capture, X11, non-Sway compositors, GPU inference, cloud transcription, telemetry, or a graphical UI.

## Build

Install runtime/build dependencies from your distribution: Go, a C compiler, `pkg-config`, `libpipewire-0.3` development headers, `wtype`, Sway, and PipeWire.

The production ASR adapter is behind the `sherpa` build tag so ordinary unit tests can run on systems that do not have sherpa's native runtime libraries on the dynamic linker path:

```sh
CGO_ENABLED=1 go build -tags "sherpa pipewire" -trimpath -ldflags "-s -w" -o sway-voice ./cmd/sway-voice
```

For development tests that do not load native ASR libraries:

```sh
go test ./...
```

## Install

```sh
install -Dm755 sway-voice "$HOME/.local/bin/sway-voice"
mkdir -p "$HOME/.config/sway-voice"
cp testdata/sample-config.toml "$HOME/.config/sway-voice/config.toml"
sway-voice model install parakeet-v3-int8
sway-voice doctor
```

The model is installed under `$HOME/.local/share/sway-voice/models/parakeet-tdt-0.6b-v3-int8` by default. The binary does not embed model assets.

## Sway Setup

Toggle mode:

```sway
exec_always sway-voice daemon
bindsym --release --no-repeat $mod+v exec sway-voice toggle
bindsym --release --no-repeat $mod+Shift+v exec sway-voice stop --discard
```

Hold-to-talk:

```sway
exec_always sway-voice daemon
bindsym --no-repeat $mod+v exec sway-voice start --mode hold
bindsym --release --no-repeat $mod+v exec sway-voice stop --commit
```

Do not add `--locked`; dictation should not intentionally run from a locked screen.

## Commands

```text
sway-voice daemon [--config PATH] [--foreground] [--log-level LEVEL]
sway-voice start [--mode toggle|oneshot|hold]
sway-voice stop [--commit|--discard]
sway-voice toggle
sway-voice status [--json]
sway-voice transcribe --file PATH [--inject]
sway-voice model check [--config PATH]
sway-voice model install parakeet-v3-int8 [--dir PATH]
sway-voice bench --file PATH [--repeat N]
sway-voice doctor
```

`transcribe --file` and `bench` do not require Sway or PipeWire, but they do require the ASR model and a sherpa-enabled build.

## Privacy Defaults

The daemon does not need network access. Model installation is the only command that downloads data. Transcripts are redacted from logs and status by default, audio segments are not saved by default, capture is paused while idle, and text injection is canceled by default if Sway focus changes between dictation start and injection.

## CPU Tuning

The default `asr.num_threads = 4` is a starting point for modern laptop CPUs. Use `sway-voice bench --file sample.wav --repeat 3` to compare real-time factor values after changing `num_threads`. The target for interactive dictation is an RTF at or below about 0.7 for short utterances on a modern 4-core/8-thread laptop CPU.

## Troubleshooting

Start with:

```sh
sway-voice doctor
sway-voice model check
sway-voice status --json
```

See [docs/troubleshooting.md](docs/troubleshooting.md) for focused checks for missing text, wrong focus, microphone issues, latency, memory use, missing model files, missing `wtype`, missing `SWAYSOCK`, and PipeWire session problems.

For production acceptance on a real Sway/PipeWire system, follow [docs/manual-testing.md](docs/manual-testing.md).
