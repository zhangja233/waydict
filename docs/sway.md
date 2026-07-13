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
bindsym --no-repeat $mod+v exec waydict start --mode hold
bindsym --release --no-repeat $mod+v exec waydict stop --commit
```

Use `--release` for toggle mode so modifier release does not race with injected text. Use `--no-repeat` so a held key does not send repeated starts. Do not use `--locked`.

The daemon uses Sway's i3-compatible IPC directly through `$SWAYSOCK` to read the focused container from `GET_TREE`. The default focus policy is `cancel_on_focus_change`: focus is recorded when dictation starts and checked again before every `wtype` invocation.

## Dictation Text

Smart casing is enabled by default:

```toml
[postprocess]
smart_case = true
```

For each VAD segment, waydict capitalizes the first word only if the segment ends in `.`, `?`, or `!` (ignoring trailing whitespace, quotes, and closing brackets) and dictation just started or the previous injected segment ended at a sentence boundary. Otherwise it lowercases the first letter, which keeps dictated words, phrases, and incomplete continuations lowercase. Sentence state spans VAD segments, advances only after text is successfully injected, and resets when a new dictation starts. Standalone `I` and contractions such as `I'm`, `I'll`, `I've`, and `I'd` are always capitalized; all-caps words of at least two letters are not lowered.

Use replacements for deterministic corrections on both Whisper and sherpa-onnx/Parakeet:

```toml
[postprocess.replacements]
cloud = "Claude"
codec = "Codex"
```

The table is empty by default. Matches are case-insensitive whole words and targets use the configured casing verbatim. This is intentionally blunt: a genuine "cloud" also becomes "Claude". Replacements run before smart casing, and a target's casing is preserved automatically — its first word joins the smart-case protect-list — so it survives even at the start of a fragment.

Vocabulary has two roles:

```toml
[asr]
vocabulary = ["Claude", "Codex"]
```

The list is empty by default. Every engine uses these terms as the case-insensitive smart-case protect-list. Whisper additionally passes them to whisper.cpp in an initial prompt, biasing the decoder so it is less likely to mishear them. sherpa-onnx/Parakeet does not use the list for decoder biasing and relies on replacements instead. Strong Whisper bias can over-fire on very short or ambiguous audio and produce a primed term that was not spoken, so keep the list focused on terms you actually dictate.

On this setup, the desktop uses Whisper and gets decoder biasing plus replacements; the laptops use Parakeet and get replacements without decoder biasing. See [gpu.md](gpu.md) for engine selection and Whisper setup.

Reloading config applies all `[postprocess]` changes, including `smart_case` and `[postprocess.replacements]`, while dictation is idle. `asr.vocabulary` is part of the Whisper engine's construction-time prompt and, like any `[asr]` change, requires a daemon restart.
