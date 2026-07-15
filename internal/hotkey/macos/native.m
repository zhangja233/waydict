//go:build darwin && cgo

#import "native.h"

#import <ApplicationServices/ApplicationServices.h>
#import <CoreFoundation/CoreFoundation.h>

#include <pthread.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <time.h>

#include "../../macos/native/waydict_events.h"

enum {
    WDHotkeyModifierControl = 1u << 0,
    WDHotkeyModifierShift = 1u << 1,
    WDHotkeyModifierOption = 1u << 2,
    WDHotkeyModifierCommand = 1u << 3,
    WDHotkeyQueueCapacity = 64,
};

typedef struct wd_hotkey_listener wd_hotkey_listener_t;

struct wd_hotkey_service {
    pthread_t thread;
    bool thread_started;
    CFRunLoopRef run_loop;
    wd_hotkey_listener_t *current;

    pthread_mutex_t state_mutex;
    pthread_cond_t state_cond;
    bool setup_done;
    int32_t setup_error;
    uint16_t initial_key_code;
    uint32_t initial_modifiers;
    int32_t initial_mode;
    bool operation_done;
    int32_t operation_error;

    pthread_mutex_t queue_wait_mutex;
    pthread_cond_t queue_wait_cond;
    wd_hotkey_event_t queue[WDHotkeyQueueCapacity];
    _Atomic(uint64_t) queue_head;
    _Atomic(uint64_t) queue_tail;

    _Atomic(bool) running;
    _Atomic(bool) terminal;
    _Atomic(uint32_t) disable_count;
    _Atomic(int32_t) last_error;
};

struct wd_hotkey_listener {
    wd_hotkey_service_t *service;
    CFMachPortRef tap;
    CFRunLoopSourceRef source;
    uint16_t key_code;
    uint32_t modifiers;
    int32_t mode;
    bool key_owned;
    double disable_times[3];
    uint64_t disable_sequence;
};

static CGEventRef wd_hotkey_callback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *context);

static CGEventMask wd_hotkey_mask(void) {
    return CGEventMaskBit(kCGEventKeyDown) |
           CGEventMaskBit(kCGEventKeyUp) |
           CGEventMaskBit(kCGEventFlagsChanged);
}

static int64_t wd_hotkey_now_ns(void) {
    struct timespec value = {0};
    if (clock_gettime(CLOCK_REALTIME, &value) != 0) {
        return 0;
    }
    return (int64_t)value.tv_sec * INT64_C(1000000000) + value.tv_nsec;
}

static double wd_hotkey_monotonic_seconds(void) {
    struct timespec value = {0};
    if (clock_gettime(CLOCK_MONOTONIC, &value) != 0) {
        return 0;
    }
    return (double)value.tv_sec + (double)value.tv_nsec / 1000000000.0;
}

static void wd_hotkey_signal_queue(wd_hotkey_service_t *service) {
    pthread_mutex_lock(&service->queue_wait_mutex);
    pthread_cond_signal(&service->queue_wait_cond);
    pthread_mutex_unlock(&service->queue_wait_mutex);
}

static void wd_hotkey_push_reserved(wd_hotkey_service_t *service, int32_t action) {
    uint64_t head = atomic_load_explicit(&service->queue_head, memory_order_relaxed);
    service->queue[head % WDHotkeyQueueCapacity] = (wd_hotkey_event_t){
        .action = action,
        .timestamp_ns = wd_hotkey_now_ns(),
    };
    atomic_store_explicit(&service->queue_head, head + 1, memory_order_release);
}

static void wd_hotkey_fail(wd_hotkey_listener_t *listener, int32_t error) {
    wd_hotkey_service_t *service = listener->service;
    if (atomic_load_explicit(&service->terminal, memory_order_acquire)) {
        return;
    }
    CGEventTapEnable(listener->tap, false);
    if (listener->key_owned && listener->mode == WDHotkeyModeHold) {
        wd_hotkey_push_reserved(service, WDHotkeyEventAbort);
    }
    listener->key_owned = false;
    atomic_store_explicit(&service->last_error, error, memory_order_release);
    atomic_store_explicit(&service->running, false, memory_order_release);
    atomic_store_explicit(&service->terminal, true, memory_order_release);
    wd_hotkey_signal_queue(service);
}

static bool wd_hotkey_push(wd_hotkey_listener_t *listener, int32_t action) {
    wd_hotkey_service_t *service = listener->service;
    uint64_t head = atomic_load_explicit(&service->queue_head, memory_order_relaxed);
    uint64_t tail = atomic_load_explicit(&service->queue_tail, memory_order_acquire);
    // One slot is reserved for a terminal Abort.
    if (head - tail >= WDHotkeyQueueCapacity - 1) {
        wd_hotkey_fail(listener, WDHotkeyErrorQueueOverflow);
        return false;
    }
    service->queue[head % WDHotkeyQueueCapacity] = (wd_hotkey_event_t){
        .action = action,
        .timestamp_ns = wd_hotkey_now_ns(),
    };
    atomic_store_explicit(&service->queue_head, head + 1, memory_order_release);
    wd_hotkey_signal_queue(service);
    return true;
}

static void wd_hotkey_abort_owned(wd_hotkey_listener_t *listener) {
    if (!listener->key_owned) {
        return;
    }
    if (listener->mode == WDHotkeyModeHold) {
        wd_hotkey_push_reserved(listener->service, WDHotkeyEventAbort);
    }
    listener->key_owned = false;
}

static uint32_t wd_hotkey_normalize_flags(CGEventFlags flags) {
    uint32_t modifiers = 0;
    if ((flags & kCGEventFlagMaskControl) != 0) {
        modifiers |= WDHotkeyModifierControl;
    }
    if ((flags & kCGEventFlagMaskShift) != 0) {
        modifiers |= WDHotkeyModifierShift;
    }
    if ((flags & kCGEventFlagMaskAlternate) != 0) {
        modifiers |= WDHotkeyModifierOption;
    }
    if ((flags & kCGEventFlagMaskCommand) != 0) {
        modifiers |= WDHotkeyModifierCommand;
    }
    return modifiers;
}

static bool wd_hotkey_disable_limit_reached(wd_hotkey_listener_t *listener) {
    double now = wd_hotkey_monotonic_seconds();
    uint64_t sequence = listener->disable_sequence++;
    listener->disable_times[sequence % 3] = now;
    if (sequence < 2) {
        return false;
    }
    return now - listener->disable_times[(sequence + 1) % 3] <= 60.0;
}

static CGEventRef wd_hotkey_callback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *context) {
    (void)proxy;
    wd_hotkey_listener_t *listener = context;
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        atomic_fetch_add_explicit(&listener->service->disable_count, 1, memory_order_relaxed);
        if (wd_hotkey_disable_limit_reached(listener)) {
            wd_hotkey_fail(listener, WDHotkeyErrorRepeatedDisable);
        } else {
            CGEventTapEnable(listener->tap, true);
            if (!CGEventTapIsEnabled(listener->tap)) {
                wd_hotkey_fail(listener, WDHotkeyErrorTapEnable);
            }
        }
        return event;
    }
    if (event == NULL || atomic_load_explicit(&listener->service->terminal, memory_order_acquire)) {
        return event;
    }
    if (CGEventGetIntegerValueField(event, kCGEventSourceUserData) == WaydictEventMarker) {
        return event;
    }
    if (type != kCGEventKeyDown && type != kCGEventKeyUp) {
        return event;
    }

    uint16_t key_code = (uint16_t)CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode);
    if (key_code != listener->key_code) {
        return event;
    }
    if (type == kCGEventKeyDown) {
        if (listener->key_owned) {
            return NULL;
        }
        bool autorepeat = CGEventGetIntegerValueField(event, kCGKeyboardEventAutorepeat) != 0;
        if (autorepeat || wd_hotkey_normalize_flags(CGEventGetFlags(event)) != listener->modifiers) {
            return event;
        }
        if (wd_hotkey_push(listener, WDHotkeyEventPress)) {
            listener->key_owned = true;
        }
        return NULL;
    }
    if (!listener->key_owned) {
        return event;
    }
    if (listener->mode != WDHotkeyModeHold || wd_hotkey_push(listener, WDHotkeyEventRelease)) {
        listener->key_owned = false;
    }
    return NULL;
}

static wd_hotkey_listener_t *wd_hotkey_listener_create(wd_hotkey_service_t *service,
                                                        uint16_t key_code,
                                                        uint32_t modifiers,
                                                        int32_t mode,
                                                        int32_t *error) {
    wd_hotkey_listener_t *listener = calloc(1, sizeof(*listener));
    if (listener == NULL) {
        *error = WDHotkeyErrorTapCreate;
        return NULL;
    }
    listener->service = service;
    listener->key_code = key_code;
    listener->modifiers = modifiers;
    listener->mode = mode;
    listener->tap = CGEventTapCreate(kCGSessionEventTap,
                                     kCGHeadInsertEventTap,
                                     kCGEventTapOptionDefault,
                                     wd_hotkey_mask(),
                                     wd_hotkey_callback,
                                     listener);
    if (listener->tap == NULL) {
        free(listener);
        *error = WDHotkeyErrorTapCreate;
        return NULL;
    }
    CGEventTapEnable(listener->tap, false);
    listener->source = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, listener->tap, 0);
    if (listener->source == NULL) {
        CFRelease(listener->tap);
        free(listener);
        *error = WDHotkeyErrorRunLoopSource;
        return NULL;
    }
    *error = WDHotkeyErrorNone;
    return listener;
}

static void wd_hotkey_listener_destroy(wd_hotkey_listener_t *listener) {
    if (listener == NULL) {
        return;
    }
    CGEventTapEnable(listener->tap, false);
    CFRelease(listener->source);
    CFRelease(listener->tap);
    free(listener);
}

static void wd_hotkey_complete_setup(wd_hotkey_service_t *service, int32_t error) {
    pthread_mutex_lock(&service->state_mutex);
    service->setup_error = error;
    service->setup_done = true;
    pthread_cond_broadcast(&service->state_cond);
    pthread_mutex_unlock(&service->state_mutex);
}

static void wd_hotkey_complete_operation(wd_hotkey_service_t *service, int32_t error) {
    pthread_mutex_lock(&service->state_mutex);
    service->operation_error = error;
    service->operation_done = true;
    pthread_cond_broadcast(&service->state_cond);
    pthread_mutex_unlock(&service->state_mutex);
}

static void *wd_hotkey_thread_main(void *context) {
    wd_hotkey_service_t *service = context;
    pthread_setname_np("waydict-hotkey");
    @autoreleasepool {
        service->run_loop = CFRunLoopGetCurrent();
        CFRetain(service->run_loop);
        int32_t error = WDHotkeyErrorNone;
        wd_hotkey_listener_t *listener = wd_hotkey_listener_create(
            service,
            service->initial_key_code,
            service->initial_modifiers,
            service->initial_mode,
            &error);
        if (listener == NULL) {
            wd_hotkey_complete_setup(service, error);
            CFRelease(service->run_loop);
            service->run_loop = NULL;
            return NULL;
        }
        service->current = listener;
        CFRunLoopAddSource(service->run_loop, listener->source, kCFRunLoopCommonModes);
        CGEventTapEnable(listener->tap, true);
        if (!CGEventTapIsEnabled(listener->tap)) {
            CFRunLoopRemoveSource(service->run_loop, listener->source, kCFRunLoopCommonModes);
            service->current = NULL;
            wd_hotkey_listener_destroy(listener);
            wd_hotkey_complete_setup(service, WDHotkeyErrorTapEnable);
            CFRelease(service->run_loop);
            service->run_loop = NULL;
            return NULL;
        }
        atomic_store_explicit(&service->running, true, memory_order_release);
        wd_hotkey_complete_setup(service, WDHotkeyErrorNone);
        CFRunLoopRun();

        listener = service->current;
        if (listener != NULL) {
            CGEventTapEnable(listener->tap, false);
            CFRunLoopRemoveSource(service->run_loop, listener->source, kCFRunLoopCommonModes);
            service->current = NULL;
            wd_hotkey_listener_destroy(listener);
        }
        atomic_store_explicit(&service->running, false, memory_order_release);
        atomic_store_explicit(&service->terminal, true, memory_order_release);
        wd_hotkey_signal_queue(service);
        CFRelease(service->run_loop);
        service->run_loop = NULL;
    }
    return NULL;
}

static CGEventRef wd_hotkey_probe_callback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *context) {
    (void)proxy;
    (void)type;
    (void)context;
    return event;
}

bool wd_hotkey_available(int32_t *native_error) {
    if (native_error != NULL) {
        *native_error = WDHotkeyErrorNone;
    }
    if (!CGPreflightListenEventAccess()) {
        if (native_error != NULL) {
            *native_error = WDHotkeyErrorPreflightDenied;
        }
        return false;
    }
    CFMachPortRef tap = CGEventTapCreate(kCGSessionEventTap,
                                        kCGHeadInsertEventTap,
                                        kCGEventTapOptionDefault,
                                        wd_hotkey_mask(),
                                        wd_hotkey_probe_callback,
                                        NULL);
    if (tap == NULL) {
        if (native_error != NULL) {
            *native_error = WDHotkeyErrorTapCreate;
        }
        return false;
    }
    CGEventTapEnable(tap, false);
    CFRelease(tap);
    return true;
}

static void wd_hotkey_free_storage(wd_hotkey_service_t *service) {
    pthread_cond_destroy(&service->queue_wait_cond);
    pthread_mutex_destroy(&service->queue_wait_mutex);
    pthread_cond_destroy(&service->state_cond);
    pthread_mutex_destroy(&service->state_mutex);
    free(service);
}

wd_hotkey_service_t *wd_hotkey_start(uint16_t key_code,
                                     uint32_t modifiers,
                                     int32_t mode,
                                     int32_t *native_error) {
    if (native_error != NULL) {
        *native_error = WDHotkeyErrorNone;
    }
    if (!CGPreflightListenEventAccess()) {
        if (native_error != NULL) {
            *native_error = WDHotkeyErrorPreflightDenied;
        }
        return NULL;
    }
    wd_hotkey_service_t *service = calloc(1, sizeof(*service));
    if (service == NULL) {
        if (native_error != NULL) {
            *native_error = WDHotkeyErrorThreadCreate;
        }
        return NULL;
    }
    pthread_mutex_init(&service->state_mutex, NULL);
    pthread_cond_init(&service->state_cond, NULL);
    pthread_mutex_init(&service->queue_wait_mutex, NULL);
    pthread_cond_init(&service->queue_wait_cond, NULL);
    atomic_init(&service->queue_head, 0);
    atomic_init(&service->queue_tail, 0);
    atomic_init(&service->running, false);
    atomic_init(&service->terminal, false);
    atomic_init(&service->disable_count, 0);
    atomic_init(&service->last_error, WDHotkeyErrorNone);

    service->initial_key_code = key_code;
    service->initial_modifiers = modifiers;
    service->initial_mode = mode;
    if (pthread_create(&service->thread, NULL, wd_hotkey_thread_main, service) != 0) {
        if (native_error != NULL) {
            *native_error = WDHotkeyErrorThreadCreate;
        }
        wd_hotkey_free_storage(service);
        return NULL;
    }
    service->thread_started = true;
    pthread_mutex_lock(&service->state_mutex);
    while (!service->setup_done) {
        pthread_cond_wait(&service->state_cond, &service->state_mutex);
    }
    int32_t error = service->setup_error;
    pthread_mutex_unlock(&service->state_mutex);
    if (error != WDHotkeyErrorNone) {
        pthread_join(service->thread, NULL);
        if (native_error != NULL) {
            *native_error = error;
        }
        wd_hotkey_free_storage(service);
        return NULL;
    }
    return service;
}

bool wd_hotkey_rebind(wd_hotkey_service_t *service,
                      uint16_t key_code,
                      uint32_t modifiers,
                      int32_t mode,
                      int32_t *native_error) {
    if (native_error != NULL) {
        *native_error = WDHotkeyErrorNone;
    }
    if (service == NULL || !atomic_load_explicit(&service->running, memory_order_acquire)) {
        if (native_error != NULL) {
            *native_error = WDHotkeyErrorNotRunning;
        }
        return false;
    }
    int32_t create_error = WDHotkeyErrorNone;
    wd_hotkey_listener_t *replacement = wd_hotkey_listener_create(service, key_code, modifiers, mode, &create_error);
    if (replacement == NULL) {
        if (native_error != NULL) {
            *native_error = create_error;
        }
        return false;
    }

    pthread_mutex_lock(&service->state_mutex);
    service->operation_done = false;
    CFRunLoopPerformBlock(service->run_loop, kCFRunLoopCommonModes, ^{
        if (!atomic_load_explicit(&service->running, memory_order_acquire)) {
            wd_hotkey_complete_operation(service, WDHotkeyErrorNotRunning);
            return;
        }
        wd_hotkey_listener_t *old = service->current;
        if (old == NULL || old->key_owned) {
            wd_hotkey_complete_operation(service, old == NULL ? WDHotkeyErrorNotRunning : WDHotkeyErrorBindingActive);
            return;
        }
        CGEventTapEnable(old->tap, false);
        CFRunLoopRemoveSource(service->run_loop, old->source, kCFRunLoopCommonModes);
        CFRunLoopAddSource(service->run_loop, replacement->source, kCFRunLoopCommonModes);
        service->current = replacement;
        CGEventTapEnable(replacement->tap, true);
        if (!CGEventTapIsEnabled(replacement->tap)) {
            CGEventTapEnable(replacement->tap, false);
            CFRunLoopRemoveSource(service->run_loop, replacement->source, kCFRunLoopCommonModes);
            service->current = old;
            CFRunLoopAddSource(service->run_loop, old->source, kCFRunLoopCommonModes);
            CGEventTapEnable(old->tap, true);
            wd_hotkey_complete_operation(service, WDHotkeyErrorTapEnable);
            return;
        }
        wd_hotkey_listener_destroy(old);
        wd_hotkey_complete_operation(service, WDHotkeyErrorNone);
    });
    CFRunLoopWakeUp(service->run_loop);
    while (!service->operation_done) {
        pthread_cond_wait(&service->state_cond, &service->state_mutex);
    }
    int32_t operation_error = service->operation_error;
    pthread_mutex_unlock(&service->state_mutex);
    if (operation_error != WDHotkeyErrorNone) {
        wd_hotkey_listener_destroy(replacement);
        if (native_error != NULL) {
            *native_error = operation_error;
        }
        return false;
    }
    return true;
}

void wd_hotkey_stop(wd_hotkey_service_t *service) {
    if (service == NULL || !service->thread_started) {
        return;
    }
    pthread_mutex_lock(&service->state_mutex);
    service->operation_done = false;
    CFRunLoopPerformBlock(service->run_loop, kCFRunLoopCommonModes, ^{
        wd_hotkey_listener_t *listener = service->current;
        if (listener != NULL) {
            wd_hotkey_abort_owned(listener);
            CGEventTapEnable(listener->tap, false);
            CFRunLoopRemoveSource(service->run_loop, listener->source, kCFRunLoopCommonModes);
        }
        atomic_store_explicit(&service->running, false, memory_order_release);
        atomic_store_explicit(&service->terminal, true, memory_order_release);
        wd_hotkey_signal_queue(service);
        wd_hotkey_complete_operation(service, WDHotkeyErrorNone);
        CFRunLoopStop(service->run_loop);
    });
    CFRunLoopWakeUp(service->run_loop);
    while (!service->operation_done) {
        pthread_cond_wait(&service->state_cond, &service->state_mutex);
    }
    pthread_mutex_unlock(&service->state_mutex);
    pthread_join(service->thread, NULL);
    service->thread_started = false;
}

int wd_hotkey_next_event(wd_hotkey_service_t *service, wd_hotkey_event_t *event) {
    if (service == NULL || event == NULL) {
        return 0;
    }
    pthread_mutex_lock(&service->queue_wait_mutex);
    for (;;) {
        uint64_t tail = atomic_load_explicit(&service->queue_tail, memory_order_relaxed);
        uint64_t head = atomic_load_explicit(&service->queue_head, memory_order_acquire);
        if (tail != head) {
            *event = service->queue[tail % WDHotkeyQueueCapacity];
            atomic_store_explicit(&service->queue_tail, tail + 1, memory_order_release);
            pthread_mutex_unlock(&service->queue_wait_mutex);
            return 1;
        }
        if (atomic_load_explicit(&service->terminal, memory_order_acquire)) {
            pthread_mutex_unlock(&service->queue_wait_mutex);
            return 0;
        }
        pthread_cond_wait(&service->queue_wait_cond, &service->queue_wait_mutex);
    }
}

void wd_hotkey_status(wd_hotkey_service_t *service, wd_hotkey_status_t *status) {
    if (status == NULL) {
        return;
    }
    *status = (wd_hotkey_status_t){0};
    if (service == NULL) {
        return;
    }
    status->running = atomic_load_explicit(&service->running, memory_order_acquire);
    status->disable_count = atomic_load_explicit(&service->disable_count, memory_order_relaxed);
    status->last_error = atomic_load_explicit(&service->last_error, memory_order_acquire);
}

void wd_hotkey_destroy(wd_hotkey_service_t *service) {
    if (service == NULL) {
        return;
    }
    wd_hotkey_stop(service);
    wd_hotkey_free_storage(service);
}
