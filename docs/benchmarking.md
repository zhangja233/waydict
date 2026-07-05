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
  "provider": "cpu",
  "model": "parakeet-unified-en-0.6b-fp32",
  "rss_peak_bytes": 1812344832
}
```

Use the same audio file while changing `[asr].num_threads` in the config. Keep `provider = "cpu"` for supported builds.

For the optional model test target, provide a real speech file:

```sh
WAYDICT_TEST_WAV=/path/to/sample.wav make test-model
```
