# Troubleshooting

## No Text Typed

Run `waydict doctor`. Check that `wtype` is installed, the daemon is running, the model files pass `waydict model check`, and `status --json` does not show a recent `wtype_failed`, `pipewire_unavailable`, or `recognition_failed` error.

## Wrong Window Receives Text

Keep `focus_policy = "cancel_on_focus_change"` and use the recommended `--release --no-repeat` toggle binding. If status shows `focus_changed`, the daemon intentionally withheld text because focus changed after dictation started.

## Microphone Not Detected

Check that the PipeWire user service is running and that your session has microphone permission. The supported capture backend is PipeWire only; PulseAudio, ALSA, JACK, PortAudio, portals, and `pw-record` are not used by the supported implementation.

## High Latency

Run `waydict bench --file sample.wav --repeat 3`, then adjust `[asr].num_threads`. Lower `[vad].min_silence_ms` only if words are not being cut off. The default endpoint delay is intentionally conservative.

## High Memory Use

The Parakeet v3 INT8 model is large. The target loaded RSS is under roughly 2.5 GiB on x86_64 Linux. Reduce other memory pressure before starting the daemon and avoid running multiple daemons.

## Missing Model Files

Run:

```sh
waydict model install parakeet-v3-int8
waydict model check
```

If using a manual model download, point `[asr].model_dir` at the directory containing `encoder.int8.onnx`, `decoder.int8.onnx`, `joiner.int8.onnx`, and `tokens.txt`.

## `wtype` Unavailable

Install `wtype` and ensure `[injection].wtype_path` is either `wtype` on `PATH` or an executable path. Text is passed through stdin with `wtype -d 1 -`; shell quoting is not used.

## Sway Socket Unavailable

Make sure the command runs inside the Sway session and `$SWAYSOCK` is set. `transcribe --file` and `bench` can run without Sway, but daemon dictation requires Sway when `[sway].require_sway = true`.

## PipeWire Session Issues

Ensure `$XDG_RUNTIME_DIR` is set and the PipeWire user service is active. Restart the daemon after fixing the session. PipeWire integration tests are optional and should be run only with `WAYDICT_TEST_PIPEWIRE=1`.
