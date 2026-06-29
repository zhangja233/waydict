# Sway Integration

`sway-voice` does not edit your Sway config automatically. Add bindings manually and reload Sway.

Toggle mode:

```sway
exec_always sway-voice daemon
bindsym --release --no-repeat $mod+v exec sway-voice toggle
bindsym --release --no-repeat $mod+Shift+v exec sway-voice stop --discard
```

Hold-to-talk mode:

```sway
exec_always sway-voice daemon
bindsym --no-repeat $mod+v exec sway-voice start --mode hold
bindsym --release --no-repeat $mod+v exec sway-voice stop --commit
```

Use `--release` for toggle mode so modifier release does not race with injected text. Use `--no-repeat` so a held key does not send repeated starts. Do not use `--locked`.

The daemon uses Sway's i3-compatible IPC directly through `$SWAYSOCK` to read the focused container from `GET_TREE`. The default focus policy is `cancel_on_focus_change`: focus is recorded when dictation starts and checked again before every `wtype` invocation.
