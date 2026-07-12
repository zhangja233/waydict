# Troubleshooting

## No Text Typed

Run `waydict doctor`. Check that `wtype` is installed, the daemon is running, the model files pass `waydict model check`, and `status --json` does not show a recent `wtype_failed`, `pipewire_unavailable`, or `recognition_failed` error.

## Wrong Window Receives Text

Keep `focus_policy = "cancel_on_focus_change"` and use the recommended `--release --no-repeat` toggle binding. If status shows `focus_changed`, the daemon intentionally withheld text because focus changed after dictation started.

## Microphone Not Detected

Check that the PipeWire user service is running and that your session has microphone permission. The supported capture backend is PipeWire only; PulseAudio, ALSA, JACK, PortAudio, portals, and `pw-record` are not used by the supported implementation.

## High Latency

Run `waydict bench --file sample.wav --repeat 3`. For sherpa-onnx, adjust `[asr].num_threads`; for Whisper, compare models in [gpu.md](gpu.md). Lower `[vad].min_silence_ms` only if words are not being cut off. The default endpoint delay is intentionally conservative.

## High Memory Use

The Parakeet Unified FP32 model is large. Expect materially higher RSS than the older INT8 package. Vulkan Whisper also keeps roughly 0.9–2.3 GB resident in VRAM depending on the model. Reduce other memory pressure before starting the daemon and avoid running multiple daemons.

## Daemon Says It Fell Back to Sherpa

With `engine = "auto"`, this is expected when the Whisper build or model, a Vulkan ICD, or render-node access is unavailable. Run `waydict doctor` and read the `asr resolution` line. While the daemon is running, `waydict status --json` exposes the same detail as `asr.fallback_reason` (`FallbackReason` in the API type), plus `resolved_engine`, `resolved_provider`, and `gpu_name`.

Set `engine = "whisper-cpp"` and `provider = "vulkan"` temporarily when you want a resolver or load error to be fatal instead of switching engines. Set `engine = "sherpa-onnx"` and `provider = "cpu"` when CPU recognition is intentional.

## Whisper Model Missing

The default auto/GPU and forced-Whisper configuration uses `ggml-large-v3-turbo`:

```sh
waydict model install ggml-large-v3-turbo
waydict model check
```

Restart the daemon after installation. `auto` can still use sherpa-onnx when its Parakeet model is installed; forced `whisper-cpp` fails hard when its selected model is missing.

## GPU Present but Resolution Says CPU

The GPU probe requires both a Vulkan ICD JSON file and read/write access to a `/dev/dri/renderD*` node. It searches `/run/opengl-driver/share/vulkan/icd.d` on NixOS, then `/usr/share/vulkan/icd.d` and `/etc/vulkan/icd.d`. Check that the driver installed an ICD and that the daemon user has the render-node group or ACL; a new group membership may require a new login session.

`waydict doctor` prints the configured engine, provisional resolution outcome, fallback reason, and an ICD hint. After model load, daemon status is authoritative: `resolved_engine = "whisper-cpp"` with `resolved_provider = "cpu"` means the native backend downgraded after the probe; the daemon logs `whisper-cpp backend downgraded`. A forced `whisper-cpp` configuration with `provider = "cpu"` deliberately reports CPU and does not probe Vulkan. See [gpu.md](gpu.md) for the complete setup.

## Missing Model Files

`waydict doctor` reports which models are missing. To (re)install both:

```sh
waydict model install all   # Parakeet, default Whisper, and silero VAD
waydict model check
```

A forced engine's missing **ASR** model is fatal. In `auto`, either a usable Vulkan Whisper model or the CPU Parakeet fallback is sufficient for that machine. A missing **silero VAD** model is not fatal: the daemon keeps running but degrades to the energy VAD (see next section).

If using a manual model download, point `[asr].model_dir` at the directory containing `encoder.onnx`, `encoder.weights`, `decoder.onnx`, `joiner.onnx`, and `tokens.txt`.

## Speech Cut Off or Not Detected (silero VAD missing)

The default config uses the silero VAD (`[vad] engine = "silero"`). If `silero_vad.onnx` is missing, the daemon silently falls back to the energy VAD, and the silero-scaled `[vad] threshold`/`negative_threshold` (0..1 probabilities) are then read as linear RMS — so segmentation misbehaves while the daemon otherwise looks healthy. `waydict doctor` flags this as a `WARN vad model` line. Fix:

```sh
waydict model install silero-vad
```

Then restart the daemon. To run on the energy VAD deliberately, set `[vad] engine = "energy"` and tune `threshold`/`negative_threshold` as linear RMS levels.

## `wtype` Unavailable

Install `wtype` and ensure `[injection].wtype_path` is either `wtype` on `PATH` or an executable path. Text is passed through stdin with `wtype -d 1 -`; shell quoting is not used.

## Sway Socket Unavailable

Make sure the command runs inside the Sway session and `$SWAYSOCK` is set. `transcribe --file` and `bench` can run without Sway, but daemon dictation requires Sway when `[sway].require_sway = true`.

## PipeWire Session Issues

Ensure `$XDG_RUNTIME_DIR` is set and the PipeWire user service is active. Restart the daemon after fixing the session. PipeWire integration tests are optional and should be run only with `WAYDICT_TEST_PIPEWIRE=1`.
