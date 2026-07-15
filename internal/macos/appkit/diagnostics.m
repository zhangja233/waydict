//go:build darwin && cgo

#import "host_internal.h"

static NSString *WDDiagnosticString(NSDictionary *values, NSString *key) {
    id value = values[key];
    return [value isKindOfClass:NSString.class] ? value : @"";
}

static NSString *WDRedactedPath(NSString *path) {
    NSString *home = NSHomeDirectory();
    if (home.length != 0 && [path containsString:home]) {
        return [path stringByReplacingOccurrencesOfString:home withString:@"~"];
    }
    return path ?: @"";
}

static NSString *WDYesNo(BOOL value) {
    return value ? WDLocalized(@"yes", @"yes") : WDLocalized(@"no", @"no");
}

@implementation WDHost (Diagnostics)

- (NSString *)diagnosticsReport {
    NSDictionary *view = self.viewModel ?: @{};
    NSDictionary *installation = self.installation ?: @{};
    NSMutableArray<NSString *> *lines = [NSMutableArray array];
    [lines addObject:WDLocalized(@"diagnostics.heading", @"Waydict Diagnostics")];
    [lines addObject:@""];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.version", @"Waydict version: %@"), WDDiagnosticString(view, @"version")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.commit", @"Commit: %@"), WDDiagnosticString(view, @"commit")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.build", @"Build: %@"), WDDiagnosticString(view, @"build_number")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.build_tags", @"Build tags: %@"), WDDiagnosticString(view, @"build_tags")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.architecture", @"Architecture: %@"), WDDiagnosticString(view, @"architecture")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.platform", @"Platform: %@"), WDDiagnosticString(view, @"platform")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.macos", @"macOS: %@"), NSProcessInfo.processInfo.operatingSystemVersionString]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.bundle_path", @"Bundle path: %@"), WDRedactedPath(installation[@"bundle_path"])]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.translocated", @"App translocation detected: %@"), WDYesNo([installation[@"translocated"] boolValue])]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.read_only", @"Volume read-only: %@"), WDYesNo([installation[@"read_only"] boolValue])]];
    [lines addObject:@""];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.runtime_state", @"Runtime state: %@"), WDDiagnosticString(view, @"state")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.config_path", @"Config path: %@"), WDRedactedPath(WDDiagnosticString(view, @"config_path"))]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.legacy_config", @"Legacy config: %@"), WDYesNo([view[@"legacy_config"] boolValue])]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.migration_warning", @"Migration warning: %@"), WDDiagnosticString(view, @"migration_warning")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.socket", @"Socket: %@"), WDRedactedPath(WDDiagnosticString(view, @"socket_path"))]];
    [lines addObject:@""];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.microphone", @"Microphone permission: %@"), WDDiagnosticString(view, @"microphone_permission")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.accessibility", @"Accessibility permission: %@"), WDDiagnosticString(view, @"accessibility_permission")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.input_monitoring", @"Input Monitoring permission: %@"), WDDiagnosticString(view, @"input_monitoring_permission")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.launch_at_login", @"Launch at login: %@"), WDYesNo([view[@"launch_at_login"] boolValue])]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.launch_at_login_error", @"Launch-at-login error: %@"), WDDiagnosticString(view, @"launch_at_login_error")]];
    [lines addObject:@""];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.audio", @"Audio backend: %@"), WDDiagnosticString(view, @"audio_backend")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.injection", @"Injection backend: %@"), WDDiagnosticString(view, @"injection_backend")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.focus", @"Focus backend: %@"), WDDiagnosticString(view, @"focus_backend")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.asr", @"ASR: %@ / %@ / %@"),
                      WDDiagnosticString(view, @"asr_engine"),
                      WDDiagnosticString(view, @"asr_model"),
                      WDDiagnosticString(view, @"asr_provider")]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.model_check", @"Model check: %@"), WDDiagnosticString(view, @"model_status")]];
    NSString *lastError = self.hostError.length == 0 ? WDDiagnosticString(view, @"last_error") : self.hostError;
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.last_error", @"Last error: %@"), WDRedactedPath(lastError)]];
    [lines addObject:[NSString stringWithFormat:WDLocalized(@"diagnostics.last_warning", @"Last warning: %@"), WDRedactedPath(WDDiagnosticString(view, @"last_warning"))]];
    return [lines componentsJoinedByString:@"\n"];
}

- (void)showDiagnostics {
    NSRect frame = NSMakeRect(0, 0, 680, 500);
    NSWindow *window = [[NSWindow alloc] initWithContentRect:frame
                                                  styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskResizable
                                                    backing:NSBackingStoreBuffered
                                                      defer:NO];
    window.title = WDLocalized(@"diagnostics.title", @"Waydict Diagnostics");
    window.releasedWhenClosed = NO;
    NSScrollView *scroll = [[NSScrollView alloc] initWithFrame:window.contentView.bounds];
    scroll.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    scroll.hasVerticalScroller = YES;
    NSTextView *text = [[NSTextView alloc] initWithFrame:scroll.bounds];
    text.editable = NO;
    text.selectable = YES;
    text.font = [NSFont monospacedSystemFontOfSize:12 weight:NSFontWeightRegular];
    text.string = self.diagnosticsReport;
    text.accessibilityLabel = WDLocalized(@"diagnostics.report", @"Waydict diagnostics report");
    scroll.documentView = text;
    [window.contentView addSubview:scroll];
    self.diagnosticsWindow = window;
    [window center];
    [window makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
}

- (void)copyDiagnostics {
    NSPasteboard *pasteboard = NSPasteboard.generalPasteboard;
    [pasteboard clearContents];
    [pasteboard setString:self.diagnosticsReport forType:NSPasteboardTypeString];
}

@end
