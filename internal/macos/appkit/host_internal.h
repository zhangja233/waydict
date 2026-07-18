#ifndef WAYDICT_APPKIT_HOST_INTERNAL_H
#define WAYDICT_APPKIT_HOST_INTERNAL_H

#import <AppKit/AppKit.h>

#import "../native/waydict_events.h"

static inline NSString *WDLocalized(NSString *key, NSString *fallback) {
    return [[NSBundle mainBundle] localizedStringForKey:key value:fallback table:@"Localizable"];
}

@interface WDHost : NSObject <NSApplicationDelegate, NSMenuDelegate>

@property(nonatomic, strong) NSStatusItem *statusItem;
@property(nonatomic, strong) NSMenu *menu;
@property(nonatomic, strong) NSMenuItem *statusTitleItem;
@property(nonatomic, strong) NSMenuItem *messageItem;
@property(nonatomic, strong) NSMenuItem *startItem;
@property(nonatomic, strong) NSMenuItem *stopCommitItem;
@property(nonatomic, strong) NSMenuItem *stopDiscardItem;
@property(nonatomic, strong) NSMenuItem *modeItem;
@property(nonatomic, strong) NSArray<NSMenuItem *> *modeItems;
@property(nonatomic, strong) NSMenuItem *shortcutItem;
@property(nonatomic, strong) NSMenuItem *microphoneItem;
@property(nonatomic, strong) NSMenuItem *modelItem;
@property(nonatomic, strong) NSMenuItem *downloadModelsItem;
@property(nonatomic, strong) NSMenuItem *revealModelsItem;
@property(nonatomic, strong) NSMenuItem *microphonePermissionItem;
@property(nonatomic, strong) NSMenuItem *accessibilityPermissionItem;
@property(nonatomic, strong) NSMenuItem *inputMonitoringPermissionItem;
@property(nonatomic, strong) NSMenuItem *launchAtLoginItem;
@property(nonatomic, strong) NSMenuItem *openConfigItem;
@property(nonatomic, strong) NSMenuItem *reloadItem;
@property(nonatomic, strong) NSMenuItem *restartItem;
@property(nonatomic, strong) NSMenuItem *diagnosticsItem;
@property(nonatomic, strong) NSMenuItem *openLogItem;
@property(nonatomic, strong) NSMenuItem *diagnosticsClipboardItem;
@property(nonatomic, strong) NSMenuItem *revealBundleItem;
@property(nonatomic, strong) NSMenuItem *quitItem;
@property(nonatomic, strong) NSDictionary *viewModel;
@property(nonatomic, strong) NSDictionary *installation;
@property(nonatomic, copy) NSString *hostError;
@property(nonatomic, strong) NSWindow *diagnosticsWindow;
@property(nonatomic, copy) NSString *diagnosticsText;
@property(nonatomic, strong) dispatch_source_t memoryPressureSource;
@property(nonatomic) pid_t latestExternalPID;
@property(nonatomic) BOOL terminationPending;
@property(nonatomic) BOOL terminating;
@property(nonatomic) BOOL installationAlertShown;
@property(nonatomic) NSUInteger hostErrorGeneration;

- (void)showErrorCode:(NSString *)code message:(NSString *)message;
- (BOOL)emitAction:(WaydictAppAction)action payload:(NSString *)payload number:(int64_t)number;
- (void)showBusyMessage;
- (void)showInstallationAlertIfNeeded;
- (void)revealBundle:(id)sender;

@end

@interface WDHost (Menu)
- (void)setupMenu;
- (void)applyViewModel:(NSDictionary *)viewModel;
@end

@interface WDHost (Diagnostics)
- (void)showDiagnostics;
- (void)copyDiagnostics;
- (NSString *)diagnosticsReport;
@end

#endif
