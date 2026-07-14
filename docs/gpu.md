# GPU ASR

waydict can run Whisper through a direct cgo integration with whisper.cpp 1.8.7. The GPU backend is ggml Vulkan; CUDA, ROCm, and a sherpa-onnx GPU provider are not used. The default `asr.engine = "auto"` tries Vulkan Whisper when the build, configured Whisper model, Vulkan ICD, and render-node access are present, then falls back to the unchanged sherpa-onnx CPU path with a logged and status-visible reason. A forced engine never switches engines, and its resolution or load errors are fatal.

## Requirements

- Any Vulkan-capable GPU with a working ICD and an accessible `/dev/dri/renderD*` node.
- Enough VRAM for the selected model; resident use is approximately 0.9 GB for small.en, 2.1 GB for medium.en, and 2.3 GB for large-v3-turbo.
- A waydict build with the `whispercpp` tag and a Vulkan-enabled libwhisper.
- The selected ggml model installed under the waydict-managed model root.

On AMD, Mesa RADV is the tested ICD. Development and measurements used an RX 5700 with RADV. ROCm is neither required nor used.

## Build Setup

On NixOS, the default flake package and dev shell supply the patched `whisper-cpp-vulkan`, Vulkan loader, and pkg-config metadata; the default package and Makefile enable the build tag. The `sherpa` package and dev shell omit those dependencies and the `whispercpp` tag. No ROCm or separate Whisper build setup is needed. The host still needs its normal Vulkan ICD and render-node permissions. The whisper.cpp package is patched because this cgo integration links the backends directly instead of calling dynamic backend loading.

Outside Nix, install a Vulkan-enabled libwhisper that provides `whisper.pc` to `pkg-config` at build time, plus the Vulkan loader and normal C/cgo toolchain. Build with the `whispercpp` tag shown in the README. At runtime the system ICD and `/dev/dri` access are still required.

## Configuration

The default prefers GPU Whisper and records why it used CPU sherpa-onnx instead:

```toml
[asr]
engine = "auto"
provider = ""
whisper_model = "ggml-large-v3-turbo"
gpu_device = 0
```

Force Vulkan Whisper when switching to sherpa-onnx would hide a setup error:

```toml
[asr]
engine = "whisper-cpp"
provider = "vulkan"
whisper_model = "ggml-large-v3-turbo"
gpu_device = 0
```

`provider = "cpu"` is also valid for a forced `whisper-cpp` engine. Force the existing CPU engine with:

```toml
[asr]
engine = "sherpa-onnx"
provider = "cpu"
```

Resolution happens at daemon startup. Restart after any `[asr]` change; config reload deliberately does not swap or re-resolve engines.

`whisper_model` is a bare whisper.cpp ggml model name, not a filesystem path. Any such model name can be selected; waydict resolves it under its managed models root.

## Models

The three catalog models have pinned sizes and SHA-256 checksums:

| Model/install name      | Download | Resident VRAM |
|-------------------------|---------:|--------------:|
| `ggml-small.en`         |  ~488 MB |       ~0.9 GB |
| `ggml-medium.en`        |   ~1.5 GB |       ~2.1 GB |
| `ggml-large-v3-turbo`   |   ~1.6 GB |       ~2.3 GB |

```sh
waydict model install ggml-small.en
waydict model install ggml-medium.en
waydict model install ggml-large-v3-turbo
waydict model install ggml-base.en
```

Any bare whisper.cpp ggml model name works. Non-catalog models are fetched from the upstream whisper.cpp model repository, checked against a 64 MiB plausible-size minimum, and installed with a warning that integrity is not pinned. waydict installs models at `${XDG_DATA_HOME:-~/.local/share}/waydict/models/whisper/<name>.bin`; the path is managed rather than configurable, apart from the existing install-only `--dir` override.

`waydict model install all` installs Parakeet, silero VAD, and the default `ggml-large-v3-turbo`. `waydict model check` is engine-aware: a forced engine checks its model, while `auto` reports each model that passes its file checks and succeeds when at least one does.

## Performance

Decode seconds on an RX 5700 with RADV. Results are averages of five warmed, in-process `bench --repeat 5` repetitions on JFK clips; model loading is excluded. CPU is `parakeet-unified-en-0.6b-fp32` at four threads.

| Clip | Parakeet CPU | Whisper small.en | Whisper medium.en | Whisper large-v3-turbo |
|-----:|-------------:|-----------------:|------------------:|-----------------------:|
| 1 s  |        0.137 |            0.214 |             0.638 |                  0.561 |
| 3 s  |        0.249 |            0.228 |             0.666 |                  0.543 |
| 10 s |        0.684 |            0.295 |             0.807 |                  0.591 |

GPU latency is nearly flat with clip length. CPU Parakeet wins at one second and below; the fastest GPU path wins at three seconds and above, and GPU decoding frees the CPU cores. For pure English word error rate, Parakeet 0.6B is competitive with or better than the Whisper tiers on public leaderboards. The large-v3-turbo GPU default is about flat latency, CPU offload, and the best accuracy within the Whisper family—not a claim that it beats Parakeet English accuracy. For minimum short-utterance latency or maximum English accuracy per watt, force `engine = "sherpa-onnx"`.

whisper.cpp 1.9 and newer add a Parakeet-TDT backend that could put the same model family on GPU. It is not available in the pinned nixpkgs whisper.cpp 1.8.7.

## Troubleshooting

Run `waydict doctor` first. `asr configured` shows the requested engine/provider; `asr resolution` shows the provisional engine/provider and, for an automatic fallback, the reason. `Vulkan ICD` reports whether a known ICD directory was found. A valid ICD alone is insufficient if the render node is absent or inaccessible.

With a running daemon, `waydict status` shows the resolved engine, provider, GPU, and a text fallback line. `waydict status --json` exposes `asr.resolved_engine`, `asr.resolved_provider`, `asr.gpu_name`, and `asr.fallback_reason` (`FallbackReason` in the API type). Status is authoritative after model load: Whisper with `resolved_provider = "cpu"` means the native backend downgraded after the probe, which is also logged.

If `auto` says it fell back, install the selected Whisper model, check `/run/opengl-driver/share/vulkan/icd.d`, `/usr/share/vulkan/icd.d`, or `/etc/vulkan/icd.d`, and ensure the user can open a `/dev/dri/renderD*` node read/write. Force Vulkan Whisper to turn resolver and load failures into fatal diagnostics instead of switching engines. Force sherpa-onnx when CPU operation is intentional.
