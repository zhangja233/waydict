//go:build darwin && cgo

#import "permissions.h"

#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <AVFoundation/AVFoundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <Foundation/Foundation.h>

static NSString *const WaydictMicrophoneSettingsURL = @"x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone";
static NSString *const WaydictAccessibilitySettingsURL = @"x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility";
static NSString *const WaydictInputMonitoringSettingsURL = @"x-apple.systempreferences:com.apple.preference.security?Privacy_ListenEvent";

static WaydictPermissionState waydict_microphone_state(void) {
    if (@available(macOS 10.14, *)) {
        switch ([AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio]) {
        case AVAuthorizationStatusNotDetermined:
            return WaydictPermissionStateNotDetermined;
        case AVAuthorizationStatusRestricted:
            return WaydictPermissionStateRestricted;
        case AVAuthorizationStatusDenied:
            return WaydictPermissionStateDenied;
        case AVAuthorizationStatusAuthorized:
            return WaydictPermissionStateGranted;
        }
    }
    return WaydictPermissionStateUnavailable;
}

static WaydictPermissionState waydict_accessibility_state(void) {
    if (@available(macOS 10.15, *)) {
        return AXIsProcessTrusted() && CGPreflightPostEventAccess()
            ? WaydictPermissionStateGranted
            : WaydictPermissionStateNotGranted;
    }
    return WaydictPermissionStateUnavailable;
}

static WaydictPermissionState waydict_input_monitoring_state(void) {
    if (@available(macOS 10.15, *)) {
        return CGPreflightListenEventAccess()
            ? WaydictPermissionStateGranted
            : WaydictPermissionStateNotGranted;
    }
    return WaydictPermissionStateUnavailable;
}

static NSString *waydict_settings_url_for_kind(WaydictPermissionKind kind) {
    switch (kind) {
    case WaydictPermissionKindMicrophone:
        return WaydictMicrophoneSettingsURL;
    case WaydictPermissionKindAccessibility:
        return WaydictAccessibilitySettingsURL;
    case WaydictPermissionKindInputMonitoring:
        return WaydictInputMonitoringSettingsURL;
    }
    return nil;
}

static WaydictPermissionResult waydict_open_settings(WaydictPermissionKind kind) {
    NSString *value = waydict_settings_url_for_kind(kind);
    NSURL *url = value == nil ? nil : [NSURL URLWithString:value];
    if (url == nil) {
        return WaydictPermissionResultInvalidKind;
    }
    return [[NSWorkspace sharedWorkspace] openURL:url]
        ? WaydictPermissionResultOK
        : WaydictPermissionResultOpenSettingsFailed;
}

static WaydictPermissionState waydict_request_microphone(void) {
    WaydictPermissionState current = waydict_microphone_state();
    if (current != WaydictPermissionStateNotDetermined) {
        return current;
    }

    dispatch_semaphore_t completed = dispatch_semaphore_create(0);
    __block WaydictPermissionState result = WaydictPermissionStateNotDetermined;
    [AVCaptureDevice requestAccessForMediaType:AVMediaTypeAudio completionHandler:^(BOOL granted) {
        result = granted ? WaydictPermissionStateGranted : waydict_microphone_state();
        if (result == WaydictPermissionStateNotDetermined) {
            result = WaydictPermissionStateDenied;
        }
        dispatch_semaphore_signal(completed);
    }];
    dispatch_semaphore_wait(completed, DISPATCH_TIME_FOREVER);
    return result;
}

void waydict_permissions_snapshot(waydict_permission_snapshot_t *snapshot) {
    if (snapshot == NULL) {
        return;
    }
    @autoreleasepool {
        snapshot->microphone = waydict_microphone_state();
        snapshot->accessibility = waydict_accessibility_state();
        snapshot->input_monitoring = waydict_input_monitoring_state();
    }
}

int waydict_permissions_request(int kind, int *state) {
    if (state == NULL) {
        return WaydictPermissionResultInvalidKind;
    }
    *state = WaydictPermissionStateUnavailable;
    @autoreleasepool {
        switch ((WaydictPermissionKind)kind) {
        case WaydictPermissionKindMicrophone:
            *state = waydict_request_microphone();
            return WaydictPermissionResultOK;
        case WaydictPermissionKindAccessibility:
            if (@available(macOS 10.15, *)) {
                NSDictionary *options = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
                (void)AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)options);
                (void)CGRequestPostEventAccess();
                *state = waydict_accessibility_state();
                if (*state != WaydictPermissionStateGranted) {
                    return waydict_open_settings(WaydictPermissionKindAccessibility);
                }
            }
            return WaydictPermissionResultOK;
        case WaydictPermissionKindInputMonitoring:
            if (@available(macOS 10.15, *)) {
                (void)CGRequestListenEventAccess();
                *state = waydict_input_monitoring_state();
            }
            return WaydictPermissionResultOK;
        }
    }
    return WaydictPermissionResultInvalidKind;
}

int waydict_permissions_open_settings(int kind) {
    @autoreleasepool {
        return waydict_open_settings((WaydictPermissionKind)kind);
    }
}
