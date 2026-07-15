#ifndef WAYDICT_HOTKEY_NATIVE_H
#define WAYDICT_HOTKEY_NATIVE_H

#include <stdbool.h>
#include <stdint.h>

typedef struct wd_hotkey_service wd_hotkey_service_t;

typedef enum {
    WDHotkeyModeHold = 1,
    WDHotkeyModeToggle = 2,
    WDHotkeyModeOneshot = 3,
} wd_hotkey_mode_t;

typedef enum {
    WDHotkeyEventPress = 1,
    WDHotkeyEventRelease = 2,
    WDHotkeyEventAbort = 3,
} wd_hotkey_event_action_t;

typedef enum {
    WDHotkeyErrorNone = 0,
    WDHotkeyErrorPreflightDenied = 1,
    WDHotkeyErrorTapCreate = 2,
    WDHotkeyErrorRunLoopSource = 3,
    WDHotkeyErrorThreadCreate = 4,
    WDHotkeyErrorTapEnable = 5,
    WDHotkeyErrorNotRunning = 6,
    WDHotkeyErrorQueueOverflow = 7,
    WDHotkeyErrorRepeatedDisable = 8,
    WDHotkeyErrorBindingActive = 9,
} wd_hotkey_error_t;

typedef struct {
    int32_t action;
    int64_t timestamp_ns;
} wd_hotkey_event_t;

typedef struct {
    bool running;
    uint32_t disable_count;
    int32_t last_error;
} wd_hotkey_status_t;

bool wd_hotkey_available(int32_t *native_error);
wd_hotkey_service_t *wd_hotkey_start(uint16_t key_code, uint32_t modifiers, int32_t mode, int32_t *native_error);
bool wd_hotkey_rebind(wd_hotkey_service_t *service, uint16_t key_code, uint32_t modifiers, int32_t mode, int32_t *native_error);
void wd_hotkey_stop(wd_hotkey_service_t *service);
int wd_hotkey_next_event(wd_hotkey_service_t *service, wd_hotkey_event_t *event);
void wd_hotkey_status(wd_hotkey_service_t *service, wd_hotkey_status_t *status);
void wd_hotkey_destroy(wd_hotkey_service_t *service);

#endif
