//go:build darwin && cgo

#import "host_internal.h"

#import <unistd.h>

static NSString *WDString(NSDictionary *values, NSString *key) {
    id value = values[key];
    return [value isKindOfClass:NSString.class] ? value : @"";
}

static BOOL WDBool(NSDictionary *values, NSString *key) {
    id value = values[key];
    return [value respondsToSelector:@selector(boolValue)] && [value boolValue];
}

static NSMenuItem *WDItem(NSString *title, SEL action, id target) {
    NSMenuItem *item = [[NSMenuItem alloc] initWithTitle:title action:action keyEquivalent:@""];
    item.target = target;
    return item;
}

static NSString *WDPermissionState(NSString *state) {
    if ([state isEqualToString:@"granted"]) {
        return WDLocalized(@"permission.granted", @"Granted");
    }
    if ([state isEqualToString:@"not_determined"]) {
        return WDLocalized(@"permission.not_requested", @"Not Requested");
    }
    if ([state isEqualToString:@"not_granted"]) {
        return WDLocalized(@"permission.not_granted", @"Not Granted");
    }
    if ([state isEqualToString:@"denied"]) {
        return WDLocalized(@"permission.denied", @"Denied");
    }
    if ([state isEqualToString:@"restricted"]) {
        return WDLocalized(@"permission.restricted", @"Restricted");
    }
    return WDLocalized(@"permission.unavailable", @"Unavailable");
}

static NSString *WDPermissionTitle(NSString *name, NSString *state) {
    return [NSString stringWithFormat:WDLocalized(@"permission.format", @"%@: %@"), name, WDPermissionState(state)];
}

@implementation WDHost (Menu)

- (void)setupMenu {
    self.statusItem = [NSStatusBar.systemStatusBar statusItemWithLength:NSVariableStatusItemLength];
    self.statusItem.button.toolTip = WDLocalized(@"app.name", @"Waydict");

    self.menu = [[NSMenu alloc] initWithTitle:WDLocalized(@"app.name", @"Waydict")];
    self.menu.delegate = self;
    self.statusItem.menu = self.menu;

    self.statusTitleItem = WDItem(@"", nil, nil);
    self.statusTitleItem.enabled = NO;
    [self.menu addItem:self.statusTitleItem];
    self.messageItem = WDItem(@"", nil, nil);
    self.messageItem.enabled = NO;
    self.messageItem.hidden = YES;
    [self.menu addItem:self.messageItem];
    [self.menu addItem:NSMenuItem.separatorItem];

    self.startItem = WDItem(WDLocalized(@"start_dictation", @"Start Dictation"), @selector(startDictation:), self);
    [self.menu addItem:self.startItem];
    self.stopCommitItem = WDItem(WDLocalized(@"stop_type", @"Stop and Type"), @selector(performAction:), self);
    self.stopCommitItem.tag = WaydictActionStopCommit;
    [self.menu addItem:self.stopCommitItem];
    self.stopDiscardItem = WDItem(WDLocalized(@"stop_discard", @"Stop and Discard"), @selector(performAction:), self);
    self.stopDiscardItem.tag = WaydictActionStopDiscard;
    [self.menu addItem:self.stopDiscardItem];

    NSMenu *modeMenu = [[NSMenu alloc] initWithTitle:WDLocalized(@"mode", @"Mode")];
    NSMutableArray<NSMenuItem *> *modeItems = [NSMutableArray array];
    NSArray<NSArray<NSString *> *> *modes = @[
        @[WDLocalized(@"mode.hold", @"Hold"), @"hold"],
        @[WDLocalized(@"mode.toggle", @"Toggle"), @"toggle"],
        @[WDLocalized(@"mode.oneshot", @"Oneshot"), @"oneshot"],
    ];
    for (NSArray<NSString *> *mode in modes) {
        NSMenuItem *item = WDItem(mode[0], @selector(performAction:), self);
        item.tag = WaydictActionSetHotkeyMode;
        item.representedObject = mode[1];
        [modeMenu addItem:item];
        [modeItems addObject:item];
    }
    self.modeItems = modeItems;
    self.modeItem = WDItem(WDLocalized(@"mode", @"Mode"), nil, nil);
    self.modeItem.submenu = modeMenu;
    [self.menu addItem:self.modeItem];
    self.shortcutItem = WDItem(WDLocalized(@"shortcut.default", @"Shortcut: ⌃⇧⌘Space"), nil, nil);
    self.shortcutItem.enabled = NO;
    [self.menu addItem:self.shortcutItem];
    [self.menu addItem:NSMenuItem.separatorItem];

    self.microphoneItem = WDItem(WDLocalized(@"microphone", @"Microphone"), nil, nil);
    self.microphoneItem.submenu = [[NSMenu alloc] initWithTitle:WDLocalized(@"microphone", @"Microphone")];
    [self.menu addItem:self.microphoneItem];
    self.modelItem = WDItem(WDLocalized(@"model.unavailable", @"Model: unavailable"), nil, nil);
    self.modelItem.enabled = NO;
    [self.menu addItem:self.modelItem];
    self.downloadModelsItem = WDItem(WDLocalized(@"download_models", @"Download Required Models…"), @selector(performAction:), self);
    self.downloadModelsItem.tag = WaydictActionInstallRequiredModels;
    [self.menu addItem:self.downloadModelsItem];
    self.revealModelsItem = WDItem(WDLocalized(@"reveal_models", @"Reveal Models in Finder"), @selector(performAction:), self);
    self.revealModelsItem.tag = WaydictActionRevealModels;
    [self.menu addItem:self.revealModelsItem];
    [self.menu addItem:NSMenuItem.separatorItem];

    NSMenu *permissionMenu = [[NSMenu alloc] initWithTitle:WDLocalized(@"permissions", @"Permissions")];
    self.microphonePermissionItem = WDItem(@"", @selector(performAction:), self);
    self.microphonePermissionItem.tag = WaydictActionRequestMicrophonePermission;
    [permissionMenu addItem:self.microphonePermissionItem];
    self.accessibilityPermissionItem = WDItem(@"", @selector(performAction:), self);
    self.accessibilityPermissionItem.tag = WaydictActionRequestAccessibilityPermission;
    [permissionMenu addItem:self.accessibilityPermissionItem];
    self.inputMonitoringPermissionItem = WDItem(@"", @selector(performAction:), self);
    self.inputMonitoringPermissionItem.tag = WaydictActionRequestInputMonitoringPermission;
    [permissionMenu addItem:self.inputMonitoringPermissionItem];
    NSMenuItem *permissions = WDItem(WDLocalized(@"permissions", @"Permissions"), nil, nil);
    permissions.submenu = permissionMenu;
    [self.menu addItem:permissions];
    self.launchAtLoginItem = WDItem(WDLocalized(@"launch_at_login", @"Launch at Login"), @selector(toggleLaunchAtLogin:), self);
    [self.menu addItem:self.launchAtLoginItem];
    [self.menu addItem:NSMenuItem.separatorItem];

    self.openConfigItem = WDItem(WDLocalized(@"open_config", @"Open Config…"), @selector(performAction:), self);
    self.openConfigItem.tag = WaydictActionOpenConfig;
    [self.menu addItem:self.openConfigItem];
    self.reloadItem = WDItem(WDLocalized(@"reload_config", @"Reload Config"), @selector(performAction:), self);
    self.reloadItem.tag = WaydictActionReloadConfig;
    [self.menu addItem:self.reloadItem];
    self.restartItem = WDItem(WDLocalized(@"restart", @"Restart Waydict"), @selector(performAction:), self);
    self.restartItem.tag = WaydictActionRestartRuntime;
    [self.menu addItem:self.restartItem];
    self.diagnosticsItem = WDItem(WDLocalized(@"run_diagnostics", @"Run Diagnostics…"), @selector(performAction:), self);
    self.diagnosticsItem.tag = WaydictActionRunDiagnostics;
    [self.menu addItem:self.diagnosticsItem];
    self.openLogItem = WDItem(WDLocalized(@"view_log", @"View Log…"), @selector(performAction:), self);
    self.openLogItem.tag = WaydictActionOpenLog;
    [self.menu addItem:self.openLogItem];
    self.diagnosticsClipboardItem = WDItem(WDLocalized(@"copy_diagnostics", @"Copy Diagnostics"), @selector(performAction:), self);
    self.diagnosticsClipboardItem.tag = WaydictActionCopyDiagnostics;
    [self.menu addItem:self.diagnosticsClipboardItem];
    self.revealBundleItem = WDItem(WDLocalized(@"installation.reveal_menu", @"Reveal Waydict in Finder"), @selector(revealBundle:), self);
    self.revealBundleItem.hidden = YES;
    [self.menu addItem:self.revealBundleItem];
    [self.menu addItem:NSMenuItem.separatorItem];

    self.quitItem = WDItem(WDLocalized(@"quit", @"Quit Waydict"), @selector(performAction:), self);
    self.quitItem.tag = WaydictActionQuit;
    [self.menu addItem:self.quitItem];

    [self applyViewModel:self.viewModel];
}

- (void)applyViewModel:(NSDictionary *)viewModel {
    if (viewModel == nil || self.statusItem == nil) {
        return;
    }
    NSString *state = WDString(viewModel, @"state");
    NSString *symbol = @"waveform";
    NSString *title = WDLocalized(@"status.ready", @"Waydict — Ready");
    NSString *accessibility = WDLocalized(@"accessibility.ready", @"Waydict, Ready");
    BOOL actionRequired = [state isEqualToString:@"error"] || WDString(viewModel, @"last_error").length != 0 || WDBool(viewModel, @"installation_blocked") || self.hostError.length != 0;
    if (actionRequired) {
        symbol = @"exclamationmark.triangle";
        title = WDLocalized(@"status.action_required", @"Waydict — Action required");
        accessibility = WDLocalized(@"accessibility.action_required", @"Waydict, Action required");
    } else if ([state isEqualToString:@"arming"] || [state isEqualToString:@"loading"]) {
        symbol = @"hourglass";
        title = WDLocalized(@"status.preparing", @"Waydict — Preparing…");
        accessibility = WDLocalized(@"accessibility.preparing", @"Waydict, Preparing");
    } else if ([state isEqualToString:@"listening"] || [state isEqualToString:@"segment_open"]) {
        symbol = @"mic.fill";
        title = WDLocalized(@"status.listening", @"Waydict — Listening");
        accessibility = WDLocalized(@"accessibility.listening", @"Waydict, Listening");
    } else if ([state isEqualToString:@"recognizing"]) {
        symbol = @"ellipsis.bubble";
        title = WDLocalized(@"status.recognizing", @"Waydict — Recognizing…");
        accessibility = WDLocalized(@"accessibility.recognizing", @"Waydict, Recognizing");
    } else if ([state isEqualToString:@"typing"]) {
        symbol = @"keyboard";
        title = WDLocalized(@"status.typing", @"Waydict — Typing…");
        accessibility = WDLocalized(@"accessibility.typing", @"Waydict, Typing");
    }
    NSImage *image = [NSImage imageWithSystemSymbolName:symbol accessibilityDescription:accessibility];
    image.template = YES;
    self.statusItem.button.image = image;
    self.statusItem.button.accessibilityLabel = accessibility;
    self.statusTitleItem.title = title;

    NSString *message = WDString(viewModel, @"installation_message");
    if (WDBool(viewModel, @"installation_blocked")) {
        message = WDLocalized(@"installation.message", @"Move Waydict to a writable Applications folder, quit this copy, and reopen it.");
    }
    if (self.hostError.length != 0) {
        message = self.hostError;
    }
    if (message.length == 0) {
        message = WDString(viewModel, @"last_error");
    }
    if (message.length == 0) {
        message = WDString(viewModel, @"last_warning");
    }
    if (message.length == 0) {
        message = WDString(viewModel, @"launch_at_login_error");
    }
    self.messageItem.title = message;
    self.messageItem.hidden = message.length == 0;

    BOOL blocked = WDBool(viewModel, @"installation_blocked");
    BOOL active = ![state isEqualToString:@"idle"] && ![state isEqualToString:@"error"];
    NSRunningApplication *target = [NSRunningApplication runningApplicationWithProcessIdentifier:self.latestExternalPID];
    BOOL targetAvailable = target != nil && !target.terminated && target.processIdentifier != getpid();
    self.startItem.enabled = !blocked && [state isEqualToString:@"idle"] && targetAvailable;
    self.startItem.toolTip = targetAvailable ? nil : WDLocalized(@"no_target", @"No target application is available");
    self.stopCommitItem.enabled = !blocked && active;
    self.stopDiscardItem.enabled = !blocked && active;

    BOOL hotkeyAvailable = WDBool(viewModel, @"hotkey_available");
    self.modeItem.enabled = !blocked && hotkeyAvailable;
    NSString *selectedMode = WDString(viewModel, @"hotkey_mode");
    for (NSMenuItem *item in self.modeItems) {
        item.enabled = !blocked && hotkeyAvailable;
        item.state = [item.representedObject isEqual:selectedMode] ? NSControlStateValueOn : NSControlStateValueOff;
    }
    NSString *shortcut = WDString(viewModel, @"hotkey_description");
    self.shortcutItem.title = shortcut.length == 0
        ? WDLocalized(@"shortcut.default", @"Shortcut: ⌃⇧⌘Space")
        : [NSString stringWithFormat:WDLocalized(@"shortcut.format", @"Shortcut: %@"), shortcut];

    NSString *selectedDevice = WDString(viewModel, @"selected_audio_device_uid");
    BOOL deviceControlled = WDBool(viewModel, @"audio_device_controlled");
    NSMenu *microphoneMenu = [[NSMenu alloc] initWithTitle:WDLocalized(@"microphone", @"Microphone")];
    NSMenuItem *defaultMicrophone = WDItem(WDLocalized(@"system_default", @"System Default"), @selector(performAction:), self);
    defaultMicrophone.tag = WaydictActionSelectAudioDevice;
    defaultMicrophone.representedObject = @"default";
    defaultMicrophone.enabled = !blocked && !active && !deviceControlled;
    defaultMicrophone.state = selectedDevice.length == 0 ? NSControlStateValueOn : NSControlStateValueOff;
    [microphoneMenu addItem:defaultMicrophone];
    NSArray *audioDevices = [viewModel[@"audio_devices"] isKindOfClass:NSArray.class] ? viewModel[@"audio_devices"] : @[];
    for (id value in audioDevices) {
        if (![value isKindOfClass:NSDictionary.class]) {
            continue;
        }
        NSDictionary *device = value;
        NSString *identifier = WDString(device, @"id");
        NSString *name = WDString(device, @"name");
        if (identifier.length == 0 || name.length == 0) {
            continue;
        }
        BOOL connected = WDBool(device, @"connected");
        NSString *title = WDBool(device, @"default")
            ? [NSString stringWithFormat:@"%@ (%@)", name, WDLocalized(@"default", @"Default")]
            : name;
        NSMenuItem *item = WDItem(title, @selector(performAction:), self);
        item.tag = WaydictActionSelectAudioDevice;
        item.representedObject = identifier;
        item.enabled = !blocked && !active && !deviceControlled && connected;
        item.state = [selectedDevice isEqualToString:identifier] ? NSControlStateValueOn : NSControlStateValueOff;
        [microphoneMenu addItem:item];
    }
    self.microphoneItem.submenu = microphoneMenu;
    NSString *microphoneName = WDString(viewModel, @"audio_device_name");
    self.microphoneItem.title = microphoneName.length == 0
        ? WDLocalized(@"microphone", @"Microphone")
        : [NSString stringWithFormat:@"%@: %@", WDLocalized(@"microphone", @"Microphone"), microphoneName];

    NSString *engine = WDString(viewModel, @"asr_engine");
    NSString *model = WDString(viewModel, @"asr_model");
    NSString *provider = WDString(viewModel, @"asr_provider");
    NSString *modelSummary = [[@[engine, model, provider] filteredArrayUsingPredicate:[NSPredicate predicateWithBlock:^BOOL(NSString *value, NSDictionary *bindings) {
        (void)bindings;
        return value.length != 0;
    }]] componentsJoinedByString:@" / "];
    self.modelItem.title = modelSummary.length == 0
        ? WDLocalized(@"model.unavailable", @"Model: unavailable")
        : [NSString stringWithFormat:WDLocalized(@"model.format", @"Model: %@"), modelSummary];
    self.downloadModelsItem.enabled = !blocked && !WDBool(viewModel, @"installing_models");
    self.downloadModelsItem.title = WDBool(viewModel, @"installing_models")
        ? WDLocalized(@"downloading_models", @"Downloading Models…")
        : WDLocalized(@"download_models", @"Download Required Models…");
    self.revealModelsItem.enabled = !blocked;

    NSString *microphone = WDString(viewModel, @"microphone_permission");
    NSString *accessibilityPermission = WDString(viewModel, @"accessibility_permission");
    NSString *inputMonitoring = WDString(viewModel, @"input_monitoring_permission");
    self.microphonePermissionItem.title = WDPermissionTitle(WDLocalized(@"microphone", @"Microphone"), microphone);
    self.accessibilityPermissionItem.title = WDPermissionTitle(WDLocalized(@"accessibility", @"Accessibility"), accessibilityPermission);
    self.inputMonitoringPermissionItem.title = WDPermissionTitle(WDLocalized(@"input_monitoring", @"Input Monitoring"), inputMonitoring);
    self.microphonePermissionItem.enabled = !blocked && ([microphone isEqualToString:@"not_determined"] || [microphone isEqualToString:@"denied"]);
    self.accessibilityPermissionItem.enabled = !blocked && [accessibilityPermission isEqualToString:@"not_granted"];
    self.inputMonitoringPermissionItem.enabled = !blocked && [inputMonitoring isEqualToString:@"not_granted"];

    self.launchAtLoginItem.state = WDBool(viewModel, @"launch_at_login") ? NSControlStateValueOn : NSControlStateValueOff;
    self.launchAtLoginItem.enabled = !blocked && WDString(viewModel, @"launch_at_login_error").length == 0;
    self.openConfigItem.enabled = !blocked;
    self.reloadItem.enabled = !blocked;
    self.restartItem.hidden = !WDBool(viewModel, @"pending_restart");
    self.restartItem.enabled = !blocked && !active;
    self.openLogItem.enabled = !blocked;
    self.revealBundleItem.hidden = !blocked;
}

- (void)menuWillOpen:(NSMenu *)menu {
    (void)menu;
    NSRunningApplication *frontmost = NSWorkspace.sharedWorkspace.frontmostApplication;
    if (frontmost != nil && frontmost.processIdentifier != getpid() && !frontmost.terminated) {
        self.latestExternalPID = frontmost.processIdentifier;
    }
    [self applyViewModel:self.viewModel];
}

- (void)startDictation:(id)sender {
    (void)sender;
    NSRunningApplication *target = [NSRunningApplication runningApplicationWithProcessIdentifier:self.latestExternalPID];
    if (target == nil || target.terminated || target.processIdentifier == getpid()) {
        [self showErrorCode:@"focus_changed" message:WDLocalized(@"no_target", @"No target application is available")];
        return;
    }
    NSString *mode = WDString(self.viewModel, @"hotkey_mode");
    WaydictAppAction action = WaydictActionStartOneshot;
    if ([mode isEqualToString:@"hold"]) {
        action = WaydictActionStartHold;
    } else if ([mode isEqualToString:@"toggle"]) {
        action = WaydictActionToggle;
    }
    if (![self emitAction:action payload:nil number:target.processIdentifier]) {
        [self showBusyMessage];
    }
}

- (void)performAction:(NSMenuItem *)sender {
    NSString *payload = [sender.representedObject isKindOfClass:NSString.class] ? sender.representedObject : nil;
    if (![self emitAction:(WaydictAppAction)sender.tag payload:payload number:0]) {
        [self showBusyMessage];
    }
}

- (void)toggleLaunchAtLogin:(id)sender {
    (void)sender;
    NSString *payload = WDBool(self.viewModel, @"launch_at_login") ? @"false" : @"true";
    if (![self emitAction:WaydictActionSetLaunchAtLogin payload:payload number:0]) {
        [self showBusyMessage];
    }
}

@end
