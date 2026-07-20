# Remote ASR

`[asr] engine = "remote"` moves the decode step to another host's waydict daemon. Everything else stays where the microphone is: PipeWire capture, silero VAD, post-processing, and `wtype` injection all run locally, and only a completed VAD segment and its transcript cross the link. The intended shape is a CPU-only laptop borrowing a desktop's GPU, which turns a multi-second Parakeet decode into a mostly-flat GPU decode plus a round trip.

```
laptop                                     desktop
──────                                     ───────
waydict daemon                             waydict daemon
  PipeWire capture                           whisper-cpp / Vulkan (already loaded)
  silero VAD                                 serve_remote_asr = true
  asr engine = "remote" ──┐                  $XDG_RUNTIME_DIR/waydict/waydict.sock
  postprocess             │                            ▲
  wtype inject            ▼                            │
  $XDG_RUNTIME_DIR/waydict/asr-remote.sock ─ ssh -N -L ┘
```

## Transport

waydict opens no network socket for this. The client dials the Unix socket named by `[asr.remote] socket` and nothing more; making that socket reach another host is the transport's job. That keeps authentication, encryption, host keys, and NAT traversal out of waydict, and keeps the code under the repository's no-outbound-network policy — `internal/asr/remote` may import `net`, but the policy test asserts it never names a network other than `unix`.

SSH provides the forward. OpenSSH has supported Unix-socket endpoints on both ends of `-L` since 6.7, and the serving host needs no configuration beyond its normal sshd (`AllowStreamLocalForwarding` defaults to `yes`). `-L` does not shell-expand the remote path, so ask the peer where its runtime directory is rather than hardcoding a uid:

```sh
peer=gpu-host    # any ssh destination running a daemon with serve_remote_asr
peer_runtime=$(ssh "$peer" 'echo "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"')

ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
  -L "$XDG_RUNTIME_DIR/waydict/asr-remote.sock:$peer_runtime/waydict/waydict.sock" "$peer"
```

Run it under a supervisor that restarts it — a systemd user unit with `Restart=always` — and note three details that otherwise bite:

- ssh refuses to bind a socket path that already exists, so remove it before starting.
- ssh creates the socket with the process umask; use `UMask=0077` so it lands owner-only.
- The socket's directory must exist and be owner-only before ssh binds. Create it in an `ExecStartPre`; do not hand the directory to systemd's `RuntimeDirectory=`, which would delete it — and the daemon's own live control socket — when the tunnel stops.

Resolve the peer's runtime directory once at startup, as above, rather than baking a uid into the unit.

## Serving host

```toml
[daemon]
serve_remote_asr = true
```

Off by default: a daemon should not become a decode service by accident. With it on, the daemon answers the `transcribe` control command using the engine it already has loaded, so lending the GPU adds no second model to VRAM. Requests are serialized against the daemon's own dictation with the same lock, so a peer's segment queues behind a local one rather than racing it.

The peer must be able to reach the control socket, which still requires matching uid — over `ssh -L` the connecting process is sshd running as the same user, so this is the same trust boundary SSH already established.

Serving is a pure decode: no session state, no post-processing, no injection, no focus guard. The client's own daemon owns all of that.

## Client host

```toml
[asr]
engine = "remote"
# These keys now configure the fallback, not the remote decode.
model_dir = "~/.local/share/waydict/models/parakeet-unified-en-0.6b-fp32"

[asr.remote]
socket = "$XDG_RUNTIME_DIR/waydict/asr-remote.sock"   # the default; usually omit
codec = "pcm_s16le"
dial_timeout_ms = 300
request_timeout_ms = 8000
fallback = "sherpa-onnx"
```

`engine = "auto"` never resolves to `remote` — reaching another host is always explicit. Startup never probes the peer, so a laptop away from home still starts normally.

## Fallback

With `fallback = "sherpa-onnx"`, any failure of the remote attempt — dial refused, tunnel down, peer with `serve_remote_asr` off, unknown codec, mid-request stall — decodes the same segment locally instead, using the `[asr]` model keys. The fallback engine loads on first use rather than at startup, so a laptop that never goes offline never pays for a second resident model; set `[daemon] preload_model = true` to load it eagerly and avoid a one-time delay on the first offline segment.

Two timings keep the degradation quick. The dial gets its own short `dial_timeout_ms`, so an unreachable peer is detected in microseconds rather than at the request deadline. And the remote attempt spends at most half the caller's remaining deadline, so a stalled link still leaves the fallback enough time to finish inside the same overall budget — with `fallback = "none"` there is nothing to reserve for, so it spends the whole budget and fails loudly instead.

## Observing which side decoded

Both paths produce text, so the difference is otherwise invisible.

```sh
waydict status --json | jq .asr.remote
# {"socket": "...", "served": "remote", "fallback": "sherpa-onnx", "last_rtf": 0.04}
```

`served` is `remote` or `fallback` for the most recent segment, and `last_error` carries why the peer was skipped. The client's `asr decode complete` log line carries the same `served=` and `remote_error=` attributes. `waydict doctor` probes the peer and reports it as a warning when a fallback is configured — dictation still works — or a failure when it is not.

## Wire format

The request is the ordinary control frame plus a binary body: one line of JSON declaring `payload_bytes`, then exactly that many bytes. The `codec` field is `pcm_s16le` today — 16-bit little-endian mono at the segment's sample rate, about 160 KB for five seconds of speech, which is nothing on a LAN and roughly a quarter second on a slow uplink. The field exists so a compressed codec can be added later without a protocol break: a peer that does not know the name rejects the request, and the client falls back locally rather than decoding garbage.

The reply is a normal control response carrying the transcript, its token timestamps, and the decode timings. The serving host logs only byte counts and timings, and replaces its own status in the reply with a redacted one, so lending the GPU does not disclose what its user is dictating or looking at.
