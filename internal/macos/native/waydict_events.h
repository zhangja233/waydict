#ifndef WAYDICT_EVENTS_H
#define WAYDICT_EVENTS_H

#include <inttypes.h>
#include <stdint.h>

static const int64_t WaydictEventMarker = INT64_C(0x5741594449435401);

typedef enum {
    WaydictActionInvalid = 0,
    WaydictActionStartHold = 1,
    WaydictActionToggle = 2,
    WaydictActionStartOneshot = 3,
    WaydictActionStopCommit = 4,
    WaydictActionStopDiscard = 5,
    WaydictActionReloadConfig = 6,
    WaydictActionInstallRequiredModels = 7,
    WaydictActionRevealModels = 8,
    WaydictActionSelectAudioDevice = 9,
    WaydictActionSetHotkeyMode = 10,
    WaydictActionRequestMicrophonePermission = 11,
    WaydictActionRequestAccessibilityPermission = 12,
    WaydictActionRequestInputMonitoringPermission = 13,
    WaydictActionSetLaunchAtLogin = 14,
    WaydictActionOpenConfig = 15,
    WaydictActionRestartRuntime = 16,
    WaydictActionOpenLog = 17,
    WaydictActionRunDiagnostics = 18,
    WaydictActionCopyDiagnostics = 19,
    WaydictActionQuit = 20,
    WaydictActionSystemWillSleep = 21,
    WaydictActionSystemDidWake = 22,
} WaydictAppAction;

#endif
