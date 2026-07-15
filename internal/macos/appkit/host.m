//go:build darwin && cgo

#import "host.h"
#import "host_internal.h"

#import <os/lock.h>
#import <unistd.h>

#include <stdlib.h>
#include <string.h>

extern bool waydictAppkitEvent(int32_t action, const char *payload, size_t payload_length, int64_t number);

static const NSUInteger WDMaxPayloadBytes = 4 * 1024;
static os_unfair_lock WDUpdateLock = OS_UNFAIR_LOCK_INIT;
static NSDictionary *WDPendingViewModel;
static WDHost *WDPendingHost;
static BOOL WDUpdateScheduled;

static void WDOnMainSync(dispatch_block_t block) {
    if (NSThread.isMainThread) {
        block();
    } else {
        dispatch_sync(dispatch_get_main_queue(), block);
    }
}

static NSDictionary *WDInstallationInfo(void) {
    NSURL *url = NSBundle.mainBundle.bundleURL.URLByResolvingSymlinksInPath.URLByStandardizingPath;
    NSString *path = url.path ?: @"";
    NSNumber *readOnly = @NO;
    NSError *error = nil;
    if (![url getResourceValue:&readOnly forKey:NSURLVolumeIsReadOnlyKey error:&error]) {
        readOnly = @NO;
    }
    BOOL translocated = [path rangeOfString:@"/AppTranslocation/" options:NSCaseInsensitiveSearch].location != NSNotFound;
    BOOL blocked = translocated || readOnly.boolValue;
    return @{
        @"bundle_path": path,
        @"translocated": @(translocated),
        @"read_only": readOnly,
        @"blocked": @(blocked),
    };
}

@implementation WDHost

- (instancetype)init {
    self = [super init];
    if (self != nil) {
        _installation = WDInstallationInfo();
        _viewModel = @{@"state": @"arming"};
    }
    return self;
}

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    (void)notification;
    [self showInstallationAlertIfNeeded];
    if (![self.installation[@"blocked"] boolValue]) {
        [self showOnboardingIfNeeded];
    }
}

- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)sender {
    (void)sender;
    if (self.terminating) {
        return NSTerminateNow;
    }
    if (![self emitAction:WaydictActionQuit payload:nil number:0]) {
        [self showBusyMessage];
        return NSTerminateCancel;
    }
    self.terminationPending = YES;
    return NSTerminateLater;
}

- (void)workspaceDidActivate:(NSNotification *)notification {
    NSRunningApplication *application = notification.userInfo[NSWorkspaceApplicationKey];
    if (application != nil && application.processIdentifier != getpid() && !application.terminated) {
        self.latestExternalPID = application.processIdentifier;
    }
}

- (void)systemWillSleep:(NSNotification *)notification {
    (void)notification;
    if (![self emitAction:WaydictActionSystemWillSleep payload:nil number:0]) {
        [self showBusyMessage];
    }
}

- (void)systemDidWake:(NSNotification *)notification {
    (void)notification;
    if (![self emitAction:WaydictActionSystemDidWake payload:nil number:0]) {
        [self showBusyMessage];
    }
}

- (BOOL)emitAction:(WaydictAppAction)action payload:(NSString *)payload number:(int64_t)number {
    NSData *data = payload == nil ? nil : [payload dataUsingEncoding:NSUTF8StringEncoding];
    if (data.length > WDMaxPayloadBytes) {
        return NO;
    }
    return waydictAppkitEvent((int32_t)action, data.bytes, data.length, number);
}

- (void)showBusyMessage {
    [self showErrorCode:@"busy" message:WDLocalized(@"busy.action", @"Waydict is busy; action not accepted")];
}

- (void)showErrorCode:(NSString *)code message:(NSString *)message {
    if ([code isEqualToString:@"app_translocated"]) {
        message = WDLocalized(@"installation.message", @"Move Waydict to a writable Applications folder, quit this copy, and reopen it.");
    }
    self.hostError = message ?: @"";
    NSUInteger generation = ++self.hostErrorGeneration;
    [self applyViewModel:self.viewModel];
    [self.onboarding showError:message ?: @""];
    if (![code isEqualToString:@"app_translocated"]) {
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 8 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{
            if (self.hostErrorGeneration == generation) {
                self.hostError = nil;
                [self applyViewModel:self.viewModel];
            }
        });
    }
}

- (void)showInstallationAlertIfNeeded {
    if (self.installationAlertShown || ![self.installation[@"blocked"] boolValue]) {
        return;
    }
    self.installationAlertShown = YES;
    NSAlert *alert = [[NSAlert alloc] init];
    alert.messageText = WDLocalized(@"installation.title", @"Move Waydict before using dictation");
    alert.informativeText = WDLocalized(@"installation.message", @"Move Waydict to a writable Applications folder, quit this copy, and reopen it.");
    [alert addButtonWithTitle:WDLocalized(@"installation.reveal", @"Reveal in Finder")];
    [alert addButtonWithTitle:WDLocalized(@"quit", @"Quit Waydict")];
    NSModalResponse response = [alert runModal];
    if (response == NSAlertFirstButtonReturn) {
        [self revealBundle:nil];
    } else if (![self emitAction:WaydictActionQuit payload:nil number:0]) {
        [self showBusyMessage];
    }
}

- (void)revealBundle:(id)sender {
    (void)sender;
    NSString *path = self.installation[@"bundle_path"];
    if (path.length != 0) {
        [NSWorkspace.sharedWorkspace activateFileViewerSelectingURLs:@[[NSURL fileURLWithPath:path]]];
    }
}

@end

waydict_host_t waydict_host_create(void) {
    if (!NSThread.isMainThread) {
        return NULL;
    }
    @autoreleasepool {
        NSApplication *application = NSApplication.sharedApplication;
        application.activationPolicy = NSApplicationActivationPolicyAccessory;
        WDHost *host = [[WDHost alloc] init];
        application.delegate = host;
        [host setupMenu];
        [NSWorkspace.sharedWorkspace.notificationCenter addObserver:host
                                                            selector:@selector(workspaceDidActivate:)
                                                                name:NSWorkspaceDidActivateApplicationNotification
                                                              object:nil];
        [NSWorkspace.sharedWorkspace.notificationCenter addObserver:host
                                                            selector:@selector(systemWillSleep:)
                                                                name:NSWorkspaceWillSleepNotification
                                                              object:nil];
        [NSWorkspace.sharedWorkspace.notificationCenter addObserver:host
                                                            selector:@selector(systemDidWake:)
                                                                name:NSWorkspaceDidWakeNotification
                                                              object:nil];
        return (__bridge_retained void *)host;
    }
}

void waydict_host_destroy(waydict_host_t opaque) {
    if (opaque == NULL) {
        return;
    }
    WDHost *host = (__bridge_transfer WDHost *)opaque;
    [NSWorkspace.sharedWorkspace.notificationCenter removeObserver:host];
    if (NSThread.isMainThread) {
        [NSStatusBar.systemStatusBar removeStatusItem:host.statusItem];
    }
}

int waydict_host_run(waydict_host_t opaque) {
    if (opaque == NULL || !NSThread.isMainThread) {
        return 1;
    }
    @autoreleasepool {
        [NSApp run];
    }
    return 0;
}

void waydict_host_terminate(waydict_host_t opaque) {
    if (opaque == NULL) {
        return;
    }
    WDHost *host = (__bridge WDHost *)opaque;
    dispatch_async(dispatch_get_main_queue(), ^{
        host.terminating = YES;
        if (host.terminationPending) {
            host.terminationPending = NO;
            [NSApp replyToApplicationShouldTerminate:YES];
        } else {
            [NSApp terminate:nil];
        }
    });
}

void waydict_host_update_status(waydict_host_t opaque, const char *json, size_t length) {
    if (opaque == NULL || json == NULL || length == 0) {
        return;
    }
    @autoreleasepool {
        NSData *data = [NSData dataWithBytes:json length:length];
        NSDictionary *view = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
        if (![view isKindOfClass:NSDictionary.class]) {
            return;
        }
        WDHost *host = (__bridge WDHost *)opaque;
        os_unfair_lock_lock(&WDUpdateLock);
        WDPendingViewModel = view;
        WDPendingHost = host;
        BOOL schedule = !WDUpdateScheduled;
        WDUpdateScheduled = YES;
        os_unfair_lock_unlock(&WDUpdateLock);
        if (!schedule) {
            return;
        }
        dispatch_async(dispatch_get_main_queue(), ^{
            os_unfair_lock_lock(&WDUpdateLock);
            NSDictionary *pending = WDPendingViewModel;
            WDHost *pendingHost = WDPendingHost;
            WDPendingViewModel = nil;
            WDPendingHost = nil;
            WDUpdateScheduled = NO;
            os_unfair_lock_unlock(&WDUpdateLock);
            pendingHost.viewModel = pending;
            [pendingHost applyViewModel:pending];
        });
    }
}

void waydict_host_show_error(waydict_host_t opaque, const char *code, const char *message) {
    if (opaque == NULL) {
        return;
    }
    NSString *nativeCode = code == NULL ? @"internal_error" : [NSString stringWithUTF8String:code];
    NSString *nativeMessage = message == NULL ? @"" : [NSString stringWithUTF8String:message];
    WDHost *host = (__bridge WDHost *)opaque;
    dispatch_async(dispatch_get_main_queue(), ^{
        [host showErrorCode:nativeCode message:nativeMessage];
    });
}

void waydict_host_show_diagnostics(waydict_host_t opaque, bool copy_only) {
    if (opaque == NULL) {
        return;
    }
    WDHost *host = (__bridge WDHost *)opaque;
    dispatch_async(dispatch_get_main_queue(), ^{
        if (copy_only) {
            [host copyDiagnostics];
        } else {
            [host showDiagnostics];
        }
    });
}

char *waydict_host_copy_installation_json(waydict_host_t opaque) {
    if (opaque == NULL) {
        return NULL;
    }
    WDHost *host = (__bridge WDHost *)opaque;
    NSData *data = [NSJSONSerialization dataWithJSONObject:host.installation options:0 error:nil];
    if (data == nil) {
        return NULL;
    }
    NSString *json = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
    return json == nil ? NULL : strdup(json.UTF8String);
}

void waydict_host_free_string(char *value) {
    free(value);
}

bool waydict_host_open_path(waydict_host_t opaque, const char *path) {
    if (opaque == NULL || path == NULL) {
        return false;
    }
    NSString *nativePath = [NSString stringWithUTF8String:path];
    __block BOOL result = NO;
    WDOnMainSync(^{
        result = [NSWorkspace.sharedWorkspace openURL:[NSURL fileURLWithPath:nativePath]];
    });
    return result;
}

bool waydict_host_reveal_path(waydict_host_t opaque, const char *path) {
    if (opaque == NULL || path == NULL) {
        return false;
    }
    NSString *nativePath = [NSString stringWithUTF8String:path];
    __block BOOL result = NO;
    WDOnMainSync(^{
        NSURL *url = [NSURL fileURLWithPath:nativePath];
        [NSWorkspace.sharedWorkspace activateFileViewerSelectingURLs:@[url]];
        result = YES;
    });
    return result;
}

bool waydict_activate_bundle(const char *bundle_id) {
    if (bundle_id == NULL) {
        return false;
    }
    NSString *identifier = [NSString stringWithUTF8String:bundle_id];
    __block BOOL result = NO;
    WDOnMainSync(^{
        NSArray<NSRunningApplication *> *applications = [NSRunningApplication runningApplicationsWithBundleIdentifier:identifier];
        NSRunningApplication *candidate = nil;
        for (NSRunningApplication *application in applications) {
            if (!application.terminated && application.processIdentifier != getpid()) {
                candidate = application;
                break;
            }
            if (!application.terminated) {
                candidate = application;
            }
        }
        result = [candidate activateWithOptions:NSApplicationActivateIgnoringOtherApps];
    });
    return result;
}

bool waydict_activate_pid(int32_t pid) {
    __block BOOL result = NO;
    WDOnMainSync(^{
        NSRunningApplication *application = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
        if (application != nil && !application.terminated) {
            result = [application activateWithOptions:NSApplicationActivateIgnoringOtherApps];
        }
    });
    return result;
}

int32_t waydict_frontmost_pid(void) {
    __block pid_t pid = 0;
    WDOnMainSync(^{
        pid = NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier;
    });
    return (int32_t)pid;
}
