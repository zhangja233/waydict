# Benchmarking

Run file transcription benchmarks without Sway or PipeWire:

```sh
waydict bench --file sample.wav --repeat 3
```

The command prints JSON:

```json
{
  "file": "sample.wav",
  "audio_seconds": 7.42,
  "decode_seconds": 2.18,
  "rtf": 0.294,
  "threads": 4,
  "engine": "sherpa-onnx",
  "provider": "cpu",
  "model": "parakeet-unified-en-0.6b-fp32",
  "rss_peak_bytes": 1812344832
}
```

Use the same audio file while changing `[asr].num_threads`, engine, provider, or model in the config. Model loading is excluded from `decode_seconds`; repetitions run in one process.

## RX 5700 Results

Decode seconds on an AMD RX 5700 with RADV. Each cell is the average of five warmed, in-process repetitions using `bench --repeat 5`; model loading is excluded. The inputs are 1, 3, and 10 second clips cut from the JFK sample. Parakeet is `parakeet-unified-en-0.6b-fp32` on sherpa-onnx CPU with four threads; Whisper uses Vulkan.

| Clip | Parakeet CPU | Whisper small.en | Whisper medium.en | Whisper large-v3-turbo |
|-----:|-------------:|-----------------:|------------------:|-----------------------:|
| 1 s  |        0.137 |            0.214 |             0.638 |                  0.561 |
| 3 s  |        0.249 |            0.228 |             0.666 |                  0.543 |
| 10 s |        0.684 |            0.295 |             0.807 |                  0.591 |

GPU latency stays nearly flat as clips grow. CPU Parakeet wins for clips of about one second or less; the fastest GPU result wins from three seconds and GPU decoding frees the CPU cores. See [gpu.md](gpu.md) for model-selection context.

For the optional model test target, provide a real speech file:

```sh
WAYDICT_TEST_WAV=/path/to/sample.wav make test-model
```
