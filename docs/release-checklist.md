# macOS release checklist

Record the release version, build number, commit, date, operator, Go/Xcode/SDK versions, model-catalog hash, test Macs, macOS versions, hardware, power mode, microphones, and application versions in the release report. Items marked **CREDENTIAL** require the protected Developer ID/notary environment; **HARDWARE** and **MANUAL** cannot be certified by CI.

## Source, tests, and artifacts

- [ ] **AUTOMATED** The default branch is green on Linux and macOS; the release commit/tag is selected; `git status --short` is empty; `go mod verify` passes.
- [ ] **AUTOMATED** `make test`, `go test -race ./...`, `make test-macos-native`, `make test-linux-native`, and protocol/bundle tests pass without golden changes.
- [ ] **AUTOMATED** `make app-macos RELEASE=1 VERSION=… BUILD_NUMBER=…` records all pins and produces universal2 app/CLI/dylibs at macOS 13.0 with only system or bundle-relative dependencies/rpaths.
- [ ] **AUTOMATED** `make package-macos`, `make verify-macos ARTIFACT=…`, and `make smoke-macos ARTIFACT=…` pass; Info.plist and embedded CLI versions agree; the DMG contains only Waydict.app, Applications, README.txt, and LICENSE.
- [ ] **MANUAL** Review release notes, known issues, licenses, SPDX SBOM, THIRD_PARTY_NOTICES, SHA256SUMS, and the generated Homebrew cask.

## Signing, notarization, and clean install

- [ ] **CREDENTIAL** The GitHub `macos-release` environment requires reviewers, restricts deployment tags, and owns all signing/notary secrets.
- [ ] **CREDENTIAL** `make sign-macos DEVELOPER_ID_APPLICATION="Developer ID Application: …"` signs leaf dylibs/frameworks and the inner CLI first, then the outer app with hardened runtime, timestamp, and only `com.apple.security.device.audio-input`.
- [ ] **CREDENTIAL** `make notarize-macos NOTARY_PROFILE=… VERSION=…` retains app/DMG submission JSON, staples and validates both tickets, and both `spctl` assessments pass.
- [ ] **CREDENTIAL + MANUAL** Download the published DMG through a quarantine-setting browser on a clean macOS 13 machine; verify SHA-256, DMG ticket, Gatekeeper acceptance, drag install, first launch, CLI status, dictation, injection, upgrade, and uninstall. Confirm no right-click/Open bypass is needed.

## Signed permission matrix

- [ ] **CREDENTIAL + MANUAL** With the production bundle ID, test Microphone, Accessibility, and Input Monitoring from not-determined through explicit grant, deny, live revoke, reactivation/status refresh, required restart, executable replacement, and developer-owned `tccutil` reset.
- [ ] **MANUAL** Confirm launch produces no permission prompt; prompts follow an explicit action; denial is actionable without loops; the CLI never receives independent capture/injection permission; reported grants match native preflight/creation.
- [ ] **MANUAL** Exercise menu-start target capture/reactivation, expected-PID mismatch, exited target, and verify failure never starts the microphone.

## Hardware, routes, models, and applications

- [ ] **HARDWARE** Built-in mic: default/explicit UID, pause/resume, 30-minute capture, sleep/wake. Record native rate/channels/UID/latency/conversion/overruns.
- [ ] **HARDWARE** USB mic: select, unplug during idle/capture, reconnect same UID. Bluetooth: rate/route changes, disconnect, default switch. Aggregate/virtual input: enumerate/convert or actionable rejection. No-input: degraded startup and recovery.
- [ ] **HARDWARE** On arm64, load all three pinned Whisper models and confirm Metal from runtime metadata plus explicit CPU and forced-Metal failure paths. On x86_64, load/transcribe `ggml-small.en`; verify sherpa loads only bundled dylibs/models.
- [ ] **MANUAL** Exercise corrupt/truncated/wrong-checksum/missing models, concurrent and crashed installers, interrupted download, atomic activation, retained prior model, exact fallback reason, and confirm only model installation opens the network.
- [ ] **MANUAL** In TextEdit, Notes, Safari, Chromium, Terminal, Xcode/VS Code, and Electron messaging, test ASCII/punctuation, precomposed/decomposed accents, CJK, emoji/ZWJ/flags, multiline/Tab, 500 characters, all focus policies, mid-chunk focus change, and target quit. Record results in `docs/macos-compatibility.md`.
- [ ] **MANUAL** Standard macOS and browser password fields reject injection without exception.

## Performance, soak, and fault injection

- [ ] **HARDWARE** On an Apple-silicon Mac with at least 8 GiB, info logging and debug transcript/audio saving off: idle CPU averages ≤1.5% of one core for 5 minutes; no idle poll is faster than 500 ms; audio callback is paused.
- [ ] **HARDWARE** Over 100 warm starts, hotkey-to-listening p95 ≤150 ms and start-to-capture-ready p95 ≤200 ms. A 500-ASCII-character final event posts in p95 ≤500 ms. No attributable AppKit main-thread stall reaches 250 ms.
- [ ] **HARDWARE** Built-in mic has zero overruns over 30 minutes; 100 session cycles do not crash/deadlock; stabilized RSS grows ≤64 MiB from warmup 10 to session 100.
- [ ] **HARDWARE** Apple-silicon Whisper Metal `ggml-small.en` p95 RTF ≤0.8 over 10 post-warmup runs. Record Intel performance; >15% same-machine regression blocks release unless explicitly accepted in notes.
- [ ] **HARDWARE** Complete an 8-hour preloaded idle soak, a 2-hour capture/recognize/commit-discard/device-recreate/reload loop, and at least 20 sleep/wake cycles alternating idle/active.
- [ ] **HARDWARE** Repeatedly disable/re-enable the event tap and inject callback overruns, device removal, model-load failure, AX timeouts, and socket disconnects. Compare crashes, leaks, native allocations, goroutines, descriptors, and threads before/after.
- [ ] **MANUAL** Any crash, deadlock, unbounded growth, stale active state, unintended injection, or unexplained gate miss blocks publication.

## Publication

- [ ] **CREDENTIAL** Publish the final DMG, `.sha256`, `SHA256SUMS`, SPDX SBOM, notices, cask, release notes, compatibility results, and known issues from the protected workflow.
- [ ] **MANUAL** Install from the public URL and repeat launch/status/transcribe/inject smoke; preserve the final artifacts, notary logs, measurements, and signed release report.
