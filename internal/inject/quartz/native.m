//go:build darwin && cgo

#import "native.h"
#import "../../macos/native/waydict_events.h"

#import <ApplicationServices/ApplicationServices.h>
#import <Carbon/Carbon.h>
#import <CoreGraphics/CoreGraphics.h>

#include <stdlib.h>

struct wd_quartz_transaction {
    CGEventSourceRef source;
};

static void wd_mark_event(CGEventRef event) {
    CGEventSetIntegerValueField(event, kCGEventSourceUserData, WaydictEventMarker);
}

int wd_quartz_available(void) {
    return AXIsProcessTrusted() && CGPreflightPostEventAccess() ? 1 : 0;
}

int wd_quartz_transaction_create(wd_quartz_transaction **out) {
    if (out == NULL) {
        return WD_QUARTZ_RESULT_INVALID;
    }
    *out = NULL;
    CGEventSourceRef source = CGEventSourceCreate(kCGEventSourceStateHIDSystemState);
    if (source == NULL) {
        return WD_QUARTZ_RESULT_SOURCE_FAILED;
    }
    wd_quartz_transaction *transaction = calloc(1, sizeof(*transaction));
    if (transaction == NULL) {
        CFRelease(source);
        return WD_QUARTZ_RESULT_NO_MEMORY;
    }
    transaction->source = source;
    *out = transaction;
    return WD_QUARTZ_RESULT_OK;
}

void wd_quartz_transaction_destroy(wd_quartz_transaction *transaction) {
    if (transaction == NULL) {
        return;
    }
    if (transaction->source != NULL) {
        CFRelease(transaction->source);
    }
    free(transaction);
}

int wd_quartz_post_unicode(wd_quartz_transaction *transaction, const uint16_t *text, size_t length) {
    if (transaction == NULL || transaction->source == NULL || text == NULL || length == 0 || length > 20) {
        return WD_QUARTZ_RESULT_INVALID;
    }
    CGEventRef down = CGEventCreateKeyboardEvent(transaction->source, 0, true);
    CGEventRef up = CGEventCreateKeyboardEvent(transaction->source, 0, false);
    if (down == NULL || up == NULL) {
        if (down != NULL) {
            CFRelease(down);
        }
        if (up != NULL) {
            CFRelease(up);
        }
        return WD_QUARTZ_RESULT_EVENT_FAILED;
    }
    wd_mark_event(down);
    wd_mark_event(up);
    // The HID-state source inherits live modifier flags, so a still-held hotkey
    // modifier would turn injected text into shortcuts (mod+h -> Hide).
    CGEventSetFlags(down, (CGEventFlags)0);
    CGEventSetFlags(up, (CGEventFlags)0);
    CGEventKeyboardSetUnicodeString(down, length, text);
    CGEventKeyboardSetUnicodeString(up, length, text);
    CGEventPost(kCGHIDEventTap, down);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down);
    CFRelease(up);
    return WD_QUARTZ_RESULT_OK;
}

int wd_quartz_post_key(wd_quartz_transaction *transaction, uint16_t keycode) {
    if (transaction == NULL || transaction->source == NULL) {
        return WD_QUARTZ_RESULT_INVALID;
    }
    CGEventRef down = CGEventCreateKeyboardEvent(transaction->source, (CGKeyCode)keycode, true);
    CGEventRef up = CGEventCreateKeyboardEvent(transaction->source, (CGKeyCode)keycode, false);
    if (down == NULL || up == NULL) {
        if (down != NULL) {
            CFRelease(down);
        }
        if (up != NULL) {
            CFRelease(up);
        }
        return WD_QUARTZ_RESULT_EVENT_FAILED;
    }
    wd_mark_event(down);
    wd_mark_event(up);
    // Same reason as post_unicode: a held modifier would make Return/Tab into
    // mod+Return / mod+Tab (the app switcher).
    CGEventSetFlags(down, (CGEventFlags)0);
    CGEventSetFlags(up, (CGEventFlags)0);
    CGEventPost(kCGHIDEventTap, down);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down);
    CFRelease(up);
    return WD_QUARTZ_RESULT_OK;
}

uint16_t wd_quartz_return_keycode(void) {
    return kVK_Return;
}

uint16_t wd_quartz_tab_keycode(void) {
    return kVK_Tab;
}
