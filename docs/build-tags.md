# Build tags

Waydict uses three opt-in Go build tags for native integrations. A plain `go build` or `go test` does not enable any of them and selects the fallback implementations.

The `cgo` term in the constraints below is the toolchain-provided build tag. Setting a Waydict tag is not sufficient when cgo is disabled.

| Tag          | Enabled constraint             | Build-info constant           |
|--------------|--------------------------------|-------------------------------|
| `sherpa`     | `sherpa && cgo`                | `buildinfo.SherpaEnabled`     |
| `pipewire`   | `pipewire && cgo && linux`     | `buildinfo.PipeWireEnabled`   |
| `whispercpp` | `whispercpp && cgo`            | `buildinfo.WhisperEnabled`    |

The enabled implementations have these native dependencies:

- `sherpa` selects the sherpa-onnx ASR engine in `internal/asr/sherpa/offline.go` and Silero VAD in `internal/vad/sherpa_silero.go`. Both use the sherpa-onnx Go binding.
- `pipewire` selects `internal/audio/pipewire/capture_cgo.go` and its C bridge. It uses `pkg-config` package `libpipewire-0.3` and links `libm`. The tag remains disabled on non-Linux hosts.
- `whispercpp` selects the whisper.cpp engine and C bridge in `internal/asr/whispercpp`, plus the CLI hooks in `cmd/waydict/whisper_hooks.go`. It uses `pkg-config` package `whisper`.

Each constant is selected by a paired file under `internal/buildinfo`:

- `sherpa_enabled.go` sets `SherpaEnabled` to `true` for `sherpa && cgo`; `sherpa_disabled.go` sets it to `false` for `!sherpa || !cgo`.
- `pipewire_enabled.go` sets `PipeWireEnabled` to `true` for `pipewire && cgo && linux`; `pipewire_disabled.go` sets it to `false` for `!pipewire || !cgo || !linux`.
- `whisper_enabled.go` sets `WhisperEnabled` to `true` for `whispercpp && cgo`; `whisper_disabled.go` sets it to `false` for `!whispercpp || !cgo`.

These constants report compile-time inclusion only; they do not probe libraries, models, devices, or runtime availability. The corresponding packages select stubs under the inverse constraints. Untagged builds therefore keep all three constants false and do not require the native libraries.

## Make targets

`make build` defaults `BUILD_TAGS` to `sherpa pipewire whispercpp` and defaults `GO_ENV` to `CGO_ENABLED=1 CGO_CFLAGS_ALLOW=-fno-strict-overflow`. Override `BUILD_TAGS` to build a subset, for example:

```sh
make build BUILD_TAGS=sherpa
```

`make test` runs the untagged `go test ./...`. The native targets enable one integration at a time:

```sh
make test-sherpa
make test-pipewire
make test-whisper
```

`test-pipewire` also sets `WAYDICT_TEST_PIPEWIRE=1`. `test-model` first performs the default tagged build, then runs the sherpa test and model/transcription checks; it requires `WAYDICT_TEST_WAV`.
