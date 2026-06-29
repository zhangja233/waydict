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
