# Sway Integration

`waydict` does not edit your Sway config automatically. Add bindings manually and reload Sway.

Toggle mode:

```sway
exec_always waydict daemon
bindsym --release --no-repeat $mod+v exec waydict toggle
bindsym --release --no-repeat $mod+Shift+v exec waydict stop --discard
```

Hold-to-talk mode:

```sway
exec_always waydict daemon
bindsym --no-repeat F1 exec waydict start --mode hold
bindsym --release --no-repeat F1 exec waydict release
```

Hold and toggle modes buffer recognized text until release or the final toggle. VAD still splits long dictation so earlier audio can be decoded while you speak; finalization flushes the open tail and injects the ordered result once. `waydict release` is mode-guarded and cannot finalize an active toggle session. Oneshot mode injects automatically after its first segment.

Use `--release` for toggle mode so modifier release does not race with injected text. Use `--no-repeat` so a held key does not send repeated starts. Do not use `--locked`.

The daemon uses Sway's i3-compatible IPC directly through `$SWAYSOCK` to read the focused container from `GET_TREE`. The default focus policy is `cancel_on_focus_change`: focus is recorded when dictation starts and checked again before every `wtype` invocation.

## Dictation Text

Smart casing is enabled by default:

```toml
[postprocess]
smart_case = true
```

For each VAD segment, waydict capitalizes the first word only if the segment ends in `.`, `?`, or `!` (ignoring trailing whitespace, quotes, and closing brackets) and dictation just started or the previous segment ended at a sentence boundary. Otherwise it lowercases the first letter, which keeps dictated words, phrases, and incomplete continuations lowercase. Sentence state spans VAD segments and resets when a new dictation starts. Standalone `I` and contractions such as `I'm`, `I'll`, `I've`, and `I'd` are always capitalized; all-caps words of at least two letters are not lowered.

Reloading config applies all `[postprocess]` changes, including `smart_case`, while dictation is idle. See [gpu.md](gpu.md) for engine selection and Whisper setup.
