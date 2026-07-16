//go:build darwin && cgo

#import "host_internal.h"

#import <unistd.h>

@interface WDOnboardingController ()

@property(nonatomic, weak) WDHost *host;
@property(nonatomic, strong) NSStackView *contentStack;
@property(nonatomic, strong) NSTextField *stepLabel;
@property(nonatomic, strong) NSTextField *errorLabel;
@property(nonatomic, strong) NSButton *backButton;
@property(nonatomic, strong) NSButton *nextButton;
@property(nonatomic, strong) NSTextView *testTextView;
@property(nonatomic) NSInteger step;

@end

static NSTextField *WDLabel(NSString *text, BOOL heading) {
    NSTextField *label = [NSTextField wrappingLabelWithString:text];
    label.selectable = NO;
    if (heading) {
        label.font = [NSFont boldSystemFontOfSize:18];
    }
    return label;
}

static NSButton *WDButton(NSString *title, SEL action, id target) {
    NSButton *button = [NSButton buttonWithTitle:title target:target action:action];
    button.bezelStyle = NSBezelStyleRounded;
    return button;
}

@implementation WDHost (Onboarding)

- (void)showOnboardingIfNeeded {
    if ([NSProcessInfo.processInfo.environment[@"WAYDICT_TEST_SUPPRESS_ONBOARDING"] boolValue]) {
        return;
    }
    NSUserDefaults *defaults = [NSUserDefaults standardUserDefaults];
    if ([defaults integerForKey:@"onboardingCompletedVersion"] >= 1) {
        return;
    }
    if (self.onboarding == nil) {
        self.onboarding = [[WDOnboardingController alloc] initWithHost:self];
    }
    [self.onboarding show];
}

@end

@implementation WDOnboardingController

- (instancetype)initWithHost:(WDHost *)host {
    NSRect frame = NSMakeRect(0, 0, 560, 430);
    NSWindow *window = [[NSWindow alloc] initWithContentRect:frame
                                                  styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable
                                                    backing:NSBackingStoreBuffered
                                                      defer:NO];
    window.title = WDLocalized(@"onboarding.title", @"Welcome to Waydict");
    window.releasedWhenClosed = NO;
    window.autorecalculatesKeyViewLoop = YES;
    [window center];
    self = [super initWithWindow:window];
    if (self == nil) {
        return nil;
    }
    _host = host;

    NSView *root = window.contentView;
    _stepLabel = [NSTextField labelWithString:@""];
    _stepLabel.font = [NSFont systemFontOfSize:12 weight:NSFontWeightMedium];
    _stepLabel.textColor = NSColor.secondaryLabelColor;
    _stepLabel.translatesAutoresizingMaskIntoConstraints = NO;
    [root addSubview:_stepLabel];

    _contentStack = [[NSStackView alloc] initWithFrame:NSZeroRect];
    _contentStack.orientation = NSUserInterfaceLayoutOrientationVertical;
    _contentStack.alignment = NSLayoutAttributeLeading;
    _contentStack.spacing = 14;
    _contentStack.translatesAutoresizingMaskIntoConstraints = NO;
    [root addSubview:_contentStack];

    _errorLabel = [NSTextField wrappingLabelWithString:@""];
    _errorLabel.textColor = NSColor.systemRedColor;
    _errorLabel.hidden = YES;
    _errorLabel.translatesAutoresizingMaskIntoConstraints = NO;
    [root addSubview:_errorLabel];

    _backButton = WDButton(WDLocalized(@"back", @"Back"), @selector(back:), self);
    _backButton.translatesAutoresizingMaskIntoConstraints = NO;
    [root addSubview:_backButton];
    NSButton *closeButton = WDButton(WDLocalized(@"close", @"Close"), @selector(close:), self);
    closeButton.keyEquivalent = @"\e";
    closeButton.translatesAutoresizingMaskIntoConstraints = NO;
    [root addSubview:closeButton];
    _nextButton = WDButton(WDLocalized(@"continue", @"Continue"), @selector(next:), self);
    _nextButton.keyEquivalent = @"\r";
    _nextButton.translatesAutoresizingMaskIntoConstraints = NO;
    [root addSubview:_nextButton];

    [NSLayoutConstraint activateConstraints:@[
        [_stepLabel.topAnchor constraintEqualToAnchor:root.topAnchor constant:24],
        [_stepLabel.leadingAnchor constraintEqualToAnchor:root.leadingAnchor constant:28],
        [_stepLabel.trailingAnchor constraintEqualToAnchor:root.trailingAnchor constant:-28],
        [_contentStack.topAnchor constraintEqualToAnchor:_stepLabel.bottomAnchor constant:14],
        [_contentStack.leadingAnchor constraintEqualToAnchor:root.leadingAnchor constant:28],
        [_contentStack.trailingAnchor constraintEqualToAnchor:root.trailingAnchor constant:-28],
        [_errorLabel.leadingAnchor constraintEqualToAnchor:root.leadingAnchor constant:28],
        [_errorLabel.trailingAnchor constraintEqualToAnchor:root.trailingAnchor constant:-28],
        [_errorLabel.bottomAnchor constraintEqualToAnchor:_backButton.topAnchor constant:-14],
        [_backButton.leadingAnchor constraintEqualToAnchor:root.leadingAnchor constant:28],
        [_backButton.bottomAnchor constraintEqualToAnchor:root.bottomAnchor constant:-22],
        [closeButton.trailingAnchor constraintEqualToAnchor:_nextButton.leadingAnchor constant:-10],
        [closeButton.bottomAnchor constraintEqualToAnchor:root.bottomAnchor constant:-22],
        [_nextButton.trailingAnchor constraintEqualToAnchor:root.trailingAnchor constant:-28],
        [_nextButton.bottomAnchor constraintEqualToAnchor:root.bottomAnchor constant:-22],
    ]];
    [self renderStep];
    return self;
}

- (void)show {
    [self showWindow:nil];
    [NSApp activateIgnoringOtherApps:YES];
    [self.window makeKeyAndOrderFront:nil];
    [self.window makeFirstResponder:self.nextButton];
}

- (void)showError:(NSString *)message {
    self.errorLabel.stringValue = message ?: @"";
    self.errorLabel.hidden = message.length == 0;
}

- (void)refreshForViewModel {
    if (self.step == 1 && self.window.visible) {
        [self renderStep];
    }
}

- (void)renderStep {
    for (NSView *view in self.contentStack.arrangedSubviews.copy) {
        [self.contentStack removeArrangedSubview:view];
        [view removeFromSuperview];
    }
    self.stepLabel.stringValue = [NSString stringWithFormat:WDLocalized(@"onboarding.step", @"Step %ld of 6"), (long)self.step + 1];
    self.backButton.enabled = self.step > 0;
    self.nextButton.title = self.step == 5 ? WDLocalized(@"finish", @"Finish") : WDLocalized(@"continue", @"Continue");
    self.errorLabel.hidden = YES;
    self.testTextView = nil;

    switch (self.step) {
    case 0:
        [self addHeading:WDLocalized(@"onboarding.privacy.title", @"Private, local dictation")];
        [self addBody:WDLocalized(@"onboarding.privacy.body", @"Recognition runs locally. Audio is not saved by default, transcripts are redacted from logs, and model installation is the only expected network operation. Debug audio saving can be enabled only through the config file.")];
        break;
    case 1: {
        [self addHeading:WDLocalized(@"onboarding.models.title", @"Install speech models")];
        [self addBody:WDLocalized(@"onboarding.models.body", @"Install the configured Whisper model and Silero VAD, or the local CPU fallback model. You can skip this step and install models later from the menu.")];
        BOOL ready = [self.host.viewModel[@"models_ready"] boolValue];
        [self addBody:ready
            ? WDLocalized(@"onboarding.models.ready", @"The configured speech models passed validation.")
            : WDLocalized(@"onboarding.models.missing", @"Required speech models are missing or invalid.")];
        BOOL installing = [self.host.viewModel[@"installing_models"] boolValue];
        if (installing) {
            NSString *item = self.host.viewModel[@"model_install_item"] ?: @"";
            NSString *phase = self.host.viewModel[@"model_install_phase"] ?: @"";
            double percent = [self.host.viewModel[@"model_install_percent"] doubleValue];
            [self addBody:[NSString stringWithFormat:WDLocalized(@"onboarding.models.progress", @"%@ %@ %.0f%%"), item, phase, percent]];
        }
        NSButton *recommended = WDButton(WDLocalized(@"onboarding.models.recommended", @"Install Recommended"), @selector(installModels:), self);
        recommended.tag = 1;
        recommended.accessibilityLabel = WDLocalized(@"onboarding.models.recommended", @"Install Recommended");
        recommended.enabled = !installing;
        [self.contentStack addArrangedSubview:recommended];
        NSButton *fallback = WDButton(WDLocalized(@"onboarding.models.cpu", @"Install CPU Fallback"), @selector(installModels:), self);
        fallback.tag = 2;
        fallback.accessibilityLabel = WDLocalized(@"onboarding.models.cpu", @"Install CPU Fallback");
        fallback.enabled = !installing;
        [self.contentStack addArrangedSubview:fallback];
        break;
    }
    case 2:
        [self addPermissionStep:WDLocalized(@"onboarding.microphone.title", @"Microphone")
                           body:WDLocalized(@"onboarding.microphone.body", @"Waydict asks for microphone access only after you press the button below.")
                         button:WDLocalized(@"onboarding.microphone.button", @"Request Microphone Access")
                         action:@selector(requestMicrophone:)];
        break;
    case 3:
        [self addPermissionStep:WDLocalized(@"onboarding.accessibility.title", @"Accessibility")
                           body:WDLocalized(@"onboarding.accessibility.body", @"Accessibility permission lets Waydict validate the target and type recognized text. System Settings opens when manual approval is needed.")
                         button:WDLocalized(@"onboarding.accessibility.button", @"Request Accessibility Access")
                         action:@selector(requestAccessibility:)];
        break;
    case 4:
        [self addPermissionStep:WDLocalized(@"onboarding.input.title", @"Input Monitoring")
                           body:WDLocalized(@"onboarding.input.body", @"Input Monitoring enables the global shortcut. macOS may require you to quit and reopen Waydict after granting access.")
                         button:WDLocalized(@"onboarding.input.button", @"Request Input Monitoring Access")
                         action:@selector(requestInputMonitoring:)];
        break;
    default: {
        [self addHeading:WDLocalized(@"onboarding.test.title", @"Test dictation")];
        [self addBody:WDLocalized(@"onboarding.test.body", @"Dictate into the field below. This uses the normal one-shot capture, recognition, focus validation, and text-injection path.")];
        NSString *shortcut = self.host.viewModel[@"hotkey_description"] ?: @"⌃⇧⌘Space";
        [self addBody:[NSString stringWithFormat:WDLocalized(@"onboarding.test.shortcut", @"Configured shortcut: %@"), shortcut]];
        NSScrollView *scroll = [[NSScrollView alloc] initWithFrame:NSMakeRect(0, 0, 500, 100)];
        scroll.hasVerticalScroller = YES;
        scroll.borderType = NSBezelBorder;
        NSTextView *textView = [[NSTextView alloc] initWithFrame:scroll.bounds];
        textView.string = WDLocalized(@"onboarding.test.placeholder", @"Your test transcription appears here.");
        textView.accessibilityLabel = WDLocalized(@"onboarding.test.field", @"Dictation test field");
        textView.accessibilityHelp = WDLocalized(@"onboarding.test.field_help", @"Focus this field before starting the dictation test.");
        self.testTextView = textView;
        scroll.documentView = textView;
        [self.contentStack addArrangedSubview:scroll];
        [scroll.widthAnchor constraintEqualToConstant:500].active = YES;
        [scroll.heightAnchor constraintEqualToConstant:100].active = YES;
        NSButton *test = WDButton(WDLocalized(@"onboarding.test.button", @"Start Dictation Test"), @selector(startTest:), self);
        test.accessibilityLabel = WDLocalized(@"onboarding.test.button", @"Start Dictation Test");
        [self.contentStack addArrangedSubview:test];
        [self.window makeFirstResponder:textView];
        break;
    }
    }
}

- (void)addHeading:(NSString *)heading {
    [self.contentStack addArrangedSubview:WDLabel(heading, YES)];
}

- (void)addBody:(NSString *)body {
    NSTextField *label = WDLabel(body, NO);
    [self.contentStack addArrangedSubview:label];
    [label.widthAnchor constraintEqualToConstant:500].active = YES;
}

- (void)addPermissionStep:(NSString *)title body:(NSString *)body button:(NSString *)button action:(SEL)action {
    [self addHeading:title];
    [self addBody:body];
    NSButton *control = WDButton(button, action, self);
    control.accessibilityLabel = button;
    control.accessibilityHelp = body;
    [self.contentStack addArrangedSubview:control];
}

- (void)next:(id)sender {
    (void)sender;
    if (self.step < 5) {
        self.step++;
        [self renderStep];
        [self.window makeFirstResponder:self.nextButton];
        return;
    }
    NSUserDefaults *defaults = [NSUserDefaults standardUserDefaults];
    [defaults setInteger:1 forKey:@"onboardingCompletedVersion"];
    [self close:nil];
}

- (void)back:(id)sender {
    (void)sender;
    if (self.step > 0) {
        self.step--;
        [self renderStep];
        [self.window makeFirstResponder:self.nextButton];
    }
}

- (void)close:(id)sender {
    (void)sender;
    [self.window orderOut:nil];
}

- (void)sendAction:(WaydictAppAction)action {
    if (![self.host emitAction:action payload:nil number:0]) {
        [self.host showBusyMessage];
    }
}

- (void)installModels:(id)sender {
    NSButton *button = [sender isKindOfClass:NSButton.class] ? sender : nil;
    NSString *profile = button.tag == 2 ? @"cpu" : @"recommended";
    if (![self.host emitAction:WaydictActionInstallRequiredModels payload:profile number:0]) {
        [self.host showBusyMessage];
    }
}

- (void)requestMicrophone:(id)sender {
    (void)sender;
    [self sendAction:WaydictActionRequestMicrophonePermission];
}

- (void)requestAccessibility:(id)sender {
    (void)sender;
    [self sendAction:WaydictActionRequestAccessibilityPermission];
}

- (void)requestInputMonitoring:(id)sender {
    (void)sender;
    [self sendAction:WaydictActionRequestInputMonitoringPermission];
}

- (void)startTest:(id)sender {
    (void)sender;
    [self.window makeFirstResponder:self.testTextView];
    if (![self.host emitAction:WaydictActionStartOneshot payload:nil number:getpid()]) {
        [self.host showBusyMessage];
    }
}

@end
