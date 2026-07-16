# macOS application compatibility

Record the tested Waydict build, macOS version, application version, insertion result, focus-policy result, secure-field result, and any exception. Secure-field rejection may not have an exception.

| Application                 | Version | ASCII / punctuation | CJK | Accented / decomposed | Emoji / ZWJ / flags | Multiline / Tab | 500 chars | Focus policies | Secure field | Notes |
|-----------------------------|---------|---------------------|-----|-----------------------|---------------------|-----------------|-----------|----------------|--------------|-------|
| TextEdit                    | TBD     | TBD                 | TBD | TBD                   | TBD                 | TBD             | TBD       | TBD            | N/A          |       |
| Notes                       | TBD     | TBD                 | TBD | TBD                   | TBD                 | TBD             | TBD       | TBD            | N/A          |       |
| Safari                      | TBD     | TBD                 | TBD | TBD                   | TBD                 | TBD             | TBD       | TBD            | TBD          |       |
| Chromium browser            | TBD     | TBD                 | TBD | TBD                   | TBD                 | TBD             | TBD       | TBD            | TBD          |       |
| Terminal                    | TBD     | TBD                 | TBD | TBD                   | TBD                 | TBD             | TBD       | TBD            | N/A          |       |
| Xcode or Visual Studio Code | TBD     | TBD                 | TBD | TBD                   | TBD                 | TBD             | TBD       | TBD            | N/A          |       |
| Electron messaging app      | TBD     | TBD                 | TBD | TBD                   | TBD                 | TBD             | TBD       | TBD            | N/A          |       |

Manual lifecycle gates: start a session before sleep, screen lock, and fast user switching; verify it is discarded, microphone use ends, and a later start succeeds without automatic resumption. Repeat after changing the default input device and after disabling/re-enabling Input Monitoring.
