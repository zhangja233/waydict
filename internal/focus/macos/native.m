//go:build darwin && cgo

#import "native.h"

#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <Foundation/Foundation.h>

#include <pthread.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct wd_focus_record {
    uint64_t token;
    pid_t pid;
    bool secure_field;
    AXUIElementRef app;
    AXUIElementRef window;
    AXUIElementRef element;
    struct wd_focus_record *next;
} wd_focus_record;

struct wd_focus_provider {
    pthread_mutex_t mutex;
    uint64_t next_token;
    uint64_t hash_salt;
    bool closed;
    wd_focus_record *records;
};

typedef struct {
    pid_t pid;
    bool secure_field;
    AXUIElementRef app;
    AXUIElementRef window;
    AXUIElementRef element;
    char *app_id;
    char *app_name;
} wd_focus_capture;

typedef struct {
    pid_t pid;
    bool secure_field;
    AXUIElementRef window;
    AXUIElementRef element;
} wd_focus_snapshot;

static void wd_set_error(char **error, const char *message) {
    if (error != NULL) {
        *error = message == NULL ? NULL : strdup(message);
    }
}

static void wd_record_destroy(wd_focus_record *record) {
    if (record == NULL) {
        return;
    }
    if (record->app != NULL) {
        CFRelease(record->app);
    }
    if (record->window != NULL) {
        CFRelease(record->window);
    }
    if (record->element != NULL) {
        CFRelease(record->element);
    }
    free(record);
}

static void wd_capture_clear(wd_focus_capture *capture) {
    if (capture == NULL) {
        return;
    }
    if (capture->app != NULL) {
        CFRelease(capture->app);
    }
    if (capture->window != NULL) {
        CFRelease(capture->window);
    }
    if (capture->element != NULL) {
        CFRelease(capture->element);
    }
    free(capture->app_id);
    free(capture->app_name);
    memset(capture, 0, sizeof(*capture));
}

static void wd_snapshot_clear(wd_focus_snapshot *snapshot) {
    if (snapshot == NULL) {
        return;
    }
    if (snapshot->window != NULL) {
        CFRelease(snapshot->window);
    }
    if (snapshot->element != NULL) {
        CFRelease(snapshot->element);
    }
    memset(snapshot, 0, sizeof(*snapshot));
}

static int wd_ax_result(AXError result) {
    switch (result) {
    case kAXErrorSuccess:
    case kAXErrorNoValue:
    case kAXErrorAttributeUnsupported:
        return WD_FOCUS_RESULT_OK;
    case kAXErrorAPIDisabled:
        return WD_FOCUS_RESULT_PERMISSION;
    case kAXErrorCannotComplete:
    case kAXErrorFailure:
    case kAXErrorInvalidUIElement:
        return WD_FOCUS_RESULT_TRANSIENT;
    default:
        return WD_FOCUS_RESULT_UNAVAILABLE;
    }
}

static int wd_copy_element_attribute(AXUIElementRef source, CFStringRef attribute, AXUIElementRef *out) {
    *out = NULL;
    CFTypeRef value = NULL;
    AXError error = AXUIElementCopyAttributeValue(source, attribute, &value);
    int result = wd_ax_result(error);
    if (error != kAXErrorSuccess) {
        if (value != NULL) {
            CFRelease(value);
        }
        return result;
    }
    if (value == NULL || CFGetTypeID(value) != AXUIElementGetTypeID()) {
        if (value != NULL) {
            CFRelease(value);
        }
        return WD_FOCUS_RESULT_OK;
    }
    *out = (AXUIElementRef)value;
    return WD_FOCUS_RESULT_OK;
}

static int wd_copy_string_attribute(AXUIElementRef source, CFStringRef attribute, CFStringRef *out) {
    *out = NULL;
    CFTypeRef value = NULL;
    AXError error = AXUIElementCopyAttributeValue(source, attribute, &value);
    int result = wd_ax_result(error);
    if (error != kAXErrorSuccess) {
        if (value != NULL) {
            CFRelease(value);
        }
        return result;
    }
    if (value == NULL || CFGetTypeID(value) != CFStringGetTypeID()) {
        if (value != NULL) {
            CFRelease(value);
        }
        return WD_FOCUS_RESULT_OK;
    }
    *out = (CFStringRef)value;
    return WD_FOCUS_RESULT_OK;
}

static bool wd_is_secure_role(CFStringRef role) {
    return role != NULL && CFEqual(role, kAXSecureTextFieldSubrole);
}

static int wd_secure_field(AXUIElementRef element, bool *secure) {
    *secure = false;
    if (element == NULL) {
        return WD_FOCUS_RESULT_OK;
    }
    AXUIElementRef current = (AXUIElementRef)CFRetain(element);
    for (size_t depth = 0; current != NULL && depth < 32; depth++) {
        CFStringRef role = NULL;
        CFStringRef subrole = NULL;
        int result = wd_copy_string_attribute(current, kAXRoleAttribute, &role);
        if (result == WD_FOCUS_RESULT_OK) {
            result = wd_copy_string_attribute(current, kAXSubroleAttribute, &subrole);
        }
        bool found = wd_is_secure_role(role) || wd_is_secure_role(subrole);
        if (role != NULL) {
            CFRelease(role);
        }
        if (subrole != NULL) {
            CFRelease(subrole);
        }
        if (result != WD_FOCUS_RESULT_OK || found) {
            CFRelease(current);
            *secure = found;
            return result;
        }

        AXUIElementRef parent = NULL;
        result = wd_copy_element_attribute(current, kAXParentAttribute, &parent);
        if (result != WD_FOCUS_RESULT_OK) {
            CFRelease(current);
            return result;
        }
        if (parent == NULL || CFEqual(parent, current)) {
            if (parent != NULL) {
                CFRelease(parent);
            }
            CFRelease(current);
            return WD_FOCUS_RESULT_OK;
        }
        CFRelease(current);
        current = parent;
    }
    if (current != NULL) {
        CFRelease(current);
    }
    return WD_FOCUS_RESULT_OK;
}

static uint64_t wd_hash_byte(uint64_t hash, uint8_t value) {
    return (hash ^ value) * UINT64_C(1099511628211);
}

static uint64_t wd_hash_string(uint64_t hash, CFStringRef value) {
    CFIndex length = CFStringGetLength(value);
    for (CFIndex index = 0; index < length; index++) {
        UniChar character = CFStringGetCharacterAtIndex(value, index);
        hash = wd_hash_byte(hash, (uint8_t)(character & 0xff));
        hash = wd_hash_byte(hash, (uint8_t)(character >> 8));
    }
    return hash;
}

static uint64_t wd_identifier_hash(uint64_t salt, CFStringRef window, CFStringRef element) {
    uint64_t hash = UINT64_C(1469598103934665603) ^ salt;
    hash = wd_hash_string(hash, window);
    hash = wd_hash_byte(hash, 0xff);
    return wd_hash_string(hash, element);
}

static uint64_t wd_token_hash(uint64_t salt, uint64_t token) {
    uint64_t hash = UINT64_C(1469598103934665603) ^ salt;
    for (size_t index = 0; index < sizeof(token); index++) {
        hash = wd_hash_byte(hash, (uint8_t)(token >> (index * 8)));
    }
    return hash;
}

static char *wd_format_stable_id(const char *app_id, pid_t pid, const char *kind, uint64_t hash, bool include_hash) {
    const char *app = app_id == NULL || app_id[0] == '\0' ? "unknown" : app_id;
    int length = include_hash
        ? snprintf(NULL, 0, "accessibility:%s:pid:%d:%s:%016llx", app, pid, kind, (unsigned long long)hash)
        : snprintf(NULL, 0, "accessibility:%s:pid:%d", app, pid);
    if (length < 0) {
        return NULL;
    }
    char *value = malloc((size_t)length + 1);
    if (value == NULL) {
        return NULL;
    }
    if (include_hash) {
        snprintf(value, (size_t)length + 1, "accessibility:%s:pid:%d:%s:%016llx", app, pid, kind, (unsigned long long)hash);
    } else {
        snprintf(value, (size_t)length + 1, "accessibility:%s:pid:%d", app, pid);
    }
    return value;
}

static int wd_capture_frontmost(wd_focus_capture *capture, char **error) {
    memset(capture, 0, sizeof(*capture));
    NSRunningApplication *application = [NSWorkspace sharedWorkspace].frontmostApplication;
    if (application == nil || application.processIdentifier <= 0) {
        wd_set_error(error, "frontmost application is temporarily unavailable");
        return WD_FOCUS_RESULT_TRANSIENT;
    }
    NSString *bundleIdentifier = application.bundleIdentifier ?: @"";
    NSString *localizedName = application.localizedName ?: @"";
    capture->app_id = strdup(bundleIdentifier.UTF8String ?: "");
    capture->app_name = strdup(localizedName.UTF8String ?: "");
    if (capture->app_id == NULL || capture->app_name == NULL) {
        wd_set_error(error, "could not allocate focus metadata");
        wd_capture_clear(capture);
        return WD_FOCUS_RESULT_NO_MEMORY;
    }
    capture->pid = application.processIdentifier;
    capture->app = AXUIElementCreateApplication(capture->pid);
    if (capture->app == NULL) {
        wd_set_error(error, "could not create Accessibility application element");
        wd_capture_clear(capture);
        return WD_FOCUS_RESULT_TRANSIENT;
    }

    int result = wd_copy_element_attribute(capture->app, kAXFocusedWindowAttribute, &capture->window);
    if (result == WD_FOCUS_RESULT_OK) {
        result = wd_copy_element_attribute(capture->app, kAXFocusedUIElementAttribute, &capture->element);
    }
    if (result == WD_FOCUS_RESULT_OK) {
        result = wd_secure_field(capture->element, &capture->secure_field);
    }
    if (result != WD_FOCUS_RESULT_OK) {
        wd_set_error(error, result == WD_FOCUS_RESULT_PERMISSION
            ? "Accessibility permission is not granted"
            : "Accessibility focus query did not complete");
        wd_capture_clear(capture);
    }
    return result;
}

static int wd_allocate_token(wd_focus_provider *provider, uint64_t *token, char **error) {
    pthread_mutex_lock(&provider->mutex);
    if (provider->closed || provider->next_token == UINT64_MAX) {
        pthread_mutex_unlock(&provider->mutex);
        wd_set_error(error, "focus provider is unavailable");
        return WD_FOCUS_RESULT_UNAVAILABLE;
    }
    *token = ++provider->next_token;
    pthread_mutex_unlock(&provider->mutex);
    return WD_FOCUS_RESULT_OK;
}

static int wd_register_capture(wd_focus_provider *provider, wd_focus_capture *capture, wd_focus_target *target, char **error) {
    uint64_t token = 0;
    int result = wd_allocate_token(provider, &token, error);
    if (result != WD_FOCUS_RESULT_OK) {
        return result;
    }

    char *stable_id = NULL;
    char *degraded_reason = NULL;
    bool degraded = false;
    if (capture->window == NULL || capture->element == NULL) {
        stable_id = wd_format_stable_id(capture->app_id, capture->pid, NULL, 0, false);
        degraded_reason = strdup("element_identity_unavailable");
        degraded = true;
    } else {
        CFStringRef windowIdentifier = NULL;
        CFStringRef elementIdentifier = NULL;
        result = wd_copy_string_attribute(capture->window, kAXIdentifierAttribute, &windowIdentifier);
        if (result == WD_FOCUS_RESULT_OK) {
            result = wd_copy_string_attribute(capture->element, kAXIdentifierAttribute, &elementIdentifier);
        }
        if (result == WD_FOCUS_RESULT_OK && windowIdentifier != NULL && elementIdentifier != NULL
                && CFStringGetLength(windowIdentifier) > 0 && CFStringGetLength(elementIdentifier) > 0) {
            uint64_t hash = wd_identifier_hash(provider->hash_salt, windowIdentifier, elementIdentifier);
            stable_id = wd_format_stable_id(capture->app_id, capture->pid, "ax", hash, true);
        } else if (result == WD_FOCUS_RESULT_OK) {
            uint64_t hash = wd_token_hash(provider->hash_salt, token);
            stable_id = wd_format_stable_id(capture->app_id, capture->pid, "target", hash, true);
            degraded_reason = strdup("identifier_unavailable");
            degraded = true;
        }
        if (windowIdentifier != NULL) {
            CFRelease(windowIdentifier);
        }
        if (elementIdentifier != NULL) {
            CFRelease(elementIdentifier);
        }
        if (result != WD_FOCUS_RESULT_OK) {
            wd_set_error(error, result == WD_FOCUS_RESULT_PERMISSION
                ? "Accessibility permission is not granted"
                : "Accessibility identity query did not complete");
            free(stable_id);
            free(degraded_reason);
            return result;
        }
    }
    if (stable_id == NULL || (degraded && degraded_reason == NULL)) {
        free(stable_id);
        free(degraded_reason);
        wd_set_error(error, "could not allocate focus identity");
        return WD_FOCUS_RESULT_NO_MEMORY;
    }

    wd_focus_record *record = calloc(1, sizeof(*record));
    if (record == NULL) {
        free(stable_id);
        free(degraded_reason);
        wd_set_error(error, "could not allocate focus record");
        return WD_FOCUS_RESULT_NO_MEMORY;
    }
    record->token = token;
    record->pid = capture->pid;
    record->secure_field = capture->secure_field;
    record->app = capture->app;
    record->window = capture->window;
    record->element = capture->element;

    pthread_mutex_lock(&provider->mutex);
    if (provider->closed) {
        pthread_mutex_unlock(&provider->mutex);
        record->app = NULL;
        record->window = NULL;
        record->element = NULL;
        wd_record_destroy(record);
        free(stable_id);
        free(degraded_reason);
        wd_set_error(error, "focus provider is unavailable");
        return WD_FOCUS_RESULT_UNAVAILABLE;
    }
    record->next = provider->records;
    provider->records = record;
    pthread_mutex_unlock(&provider->mutex);

    capture->app = NULL;
    capture->window = NULL;
    capture->element = NULL;
    target->token = token;
    target->pid = (int32_t)capture->pid;
    target->secure_field = capture->secure_field ? 1 : 0;
    target->stable_id = stable_id;
    target->app_id = capture->app_id;
    target->app_name = capture->app_name;
    target->degraded_reason = degraded_reason;
    capture->app_id = NULL;
    capture->app_name = NULL;
    return WD_FOCUS_RESULT_OK;
}

static int wd_snapshot_record(wd_focus_provider *provider, uint64_t token, wd_focus_snapshot *snapshot) {
    memset(snapshot, 0, sizeof(*snapshot));
    pthread_mutex_lock(&provider->mutex);
    for (wd_focus_record *record = provider->records; record != NULL; record = record->next) {
        if (record->token != token) {
            continue;
        }
        snapshot->pid = record->pid;
        snapshot->secure_field = record->secure_field;
        if (record->window != NULL) {
            snapshot->window = (AXUIElementRef)CFRetain(record->window);
        }
        if (record->element != NULL) {
            snapshot->element = (AXUIElementRef)CFRetain(record->element);
        }
        pthread_mutex_unlock(&provider->mutex);
        return WD_FOCUS_RESULT_OK;
    }
    pthread_mutex_unlock(&provider->mutex);
    return WD_FOCUS_RESULT_INVALID;
}

int wd_focus_provider_create(wd_focus_provider **out, char **error) {
    if (out == NULL) {
        wd_set_error(error, "focus provider output is missing");
        return WD_FOCUS_RESULT_INVALID;
    }
    *out = NULL;
    wd_focus_provider *provider = calloc(1, sizeof(*provider));
    if (provider == NULL) {
        wd_set_error(error, "could not allocate focus provider");
        return WD_FOCUS_RESULT_NO_MEMORY;
    }
    if (pthread_mutex_init(&provider->mutex, NULL) != 0) {
        free(provider);
        wd_set_error(error, "could not initialize focus registry");
        return WD_FOCUS_RESULT_UNAVAILABLE;
    }
    arc4random_buf(&provider->hash_salt, sizeof(provider->hash_salt));
    if (provider->hash_salt == 0) {
        provider->hash_salt = UINT64_C(0x5741594449435401);
    }
    *out = provider;
    return WD_FOCUS_RESULT_OK;
}

void wd_focus_provider_destroy(wd_focus_provider *provider) {
    if (provider == NULL) {
        return;
    }
    pthread_mutex_lock(&provider->mutex);
    provider->closed = true;
    wd_focus_record *records = provider->records;
    provider->records = NULL;
    pthread_mutex_unlock(&provider->mutex);
    while (records != NULL) {
        wd_focus_record *next = records->next;
        wd_record_destroy(records);
        records = next;
    }
    pthread_mutex_destroy(&provider->mutex);
    free(provider);
}

int wd_focus_available(void) {
    return AXIsProcessTrusted() ? 1 : 0;
}

int wd_focus_current(wd_focus_provider *provider, wd_focus_target *target, char **error) {
    if (provider == NULL || target == NULL) {
        wd_set_error(error, "focus provider is unavailable");
        return WD_FOCUS_RESULT_INVALID;
    }
    memset(target, 0, sizeof(*target));
    if (!AXIsProcessTrusted()) {
        wd_set_error(error, "Accessibility permission is not granted");
        return WD_FOCUS_RESULT_PERMISSION;
    }
    @autoreleasepool {
        wd_focus_capture capture;
        int result = wd_capture_frontmost(&capture, error);
        if (result == WD_FOCUS_RESULT_OK) {
            result = wd_register_capture(provider, &capture, target, error);
        }
        wd_capture_clear(&capture);
        return result;
    }
}

int wd_focus_same(wd_focus_provider *provider, uint64_t token, wd_focus_target *current, int *same, char **error) {
    if (provider == NULL || token == 0 || current == NULL || same == NULL) {
        wd_set_error(error, "focus target is invalid");
        return WD_FOCUS_RESULT_INVALID;
    }
    memset(current, 0, sizeof(*current));
    *same = 0;
    if (!AXIsProcessTrusted()) {
        wd_set_error(error, "Accessibility permission is not granted");
        return WD_FOCUS_RESULT_PERMISSION;
    }

    wd_focus_snapshot captured;
    int result = wd_snapshot_record(provider, token, &captured);
    if (result != WD_FOCUS_RESULT_OK) {
        wd_set_error(error, "focus target is no longer owned");
        return result;
    }
    @autoreleasepool {
        wd_focus_capture now;
        result = wd_capture_frontmost(&now, error);
        if (result == WD_FOCUS_RESULT_OK) {
            if (captured.secure_field || now.secure_field) {
                *same = 0;
            } else if (captured.pid != now.pid) {
                *same = 0;
            } else if (captured.window != NULL && now.window != NULL
                    && captured.element != NULL && now.element != NULL) {
                *same = CFEqual(captured.window, now.window) && CFEqual(captured.element, now.element);
            } else if (captured.window != NULL && now.window != NULL) {
                *same = CFEqual(captured.window, now.window);
            } else {
                *same = captured.pid == now.pid;
            }
            result = wd_register_capture(provider, &now, current, error);
        }
        wd_capture_clear(&now);
    }
    wd_snapshot_clear(&captured);
    return result;
}

void wd_focus_release(wd_focus_provider *provider, uint64_t token) {
    if (provider == NULL || token == 0) {
        return;
    }
    pthread_mutex_lock(&provider->mutex);
    wd_focus_record **link = &provider->records;
    while (*link != NULL && (*link)->token != token) {
        link = &(*link)->next;
    }
    wd_focus_record *record = *link;
    if (record != NULL) {
        *link = record->next;
    }
    pthread_mutex_unlock(&provider->mutex);
    wd_record_destroy(record);
}

void wd_focus_target_clear(wd_focus_target *target) {
    if (target == NULL) {
        return;
    }
    free(target->stable_id);
    free(target->app_id);
    free(target->app_name);
    free(target->degraded_reason);
    memset(target, 0, sizeof(*target));
}

void wd_focus_free(void *value) {
    free(value);
}
