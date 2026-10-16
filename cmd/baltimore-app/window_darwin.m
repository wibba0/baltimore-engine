// Cocoa shell for Baltimore Engine: one resizable window containing a
// WKWebView pointed at the embedded Go dashboard server, plus the standard
// macOS menus (About, Hide, Quit, Edit for copy/paste, View → Reload, Window).

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include "window_darwin.h"

@interface AppDelegate : NSObject <NSApplicationDelegate, WKNavigationDelegate, WKUIDelegate>
@property(strong) NSWindow *window;
@property(strong) WKWebView *web;
@property(copy) NSString *url;
@property(copy) NSString *appTitle;
@end

@implementation AppDelegate

- (void)applicationDidFinishLaunching:(NSNotification *)note {
    NSRect frame = NSMakeRect(0, 0, 1320, 860);
    NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
                              NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable;
    self.window = [[NSWindow alloc] initWithContentRect:frame
                                              styleMask:style
                                                backing:NSBackingStoreBuffered
                                                  defer:NO];
    self.window.title = self.appTitle;
    self.window.minSize = NSMakeSize(960, 620);
    self.window.releasedWhenClosed = NO;

    WKWebViewConfiguration *cfg = [[WKWebViewConfiguration alloc] init];
    self.web = [[WKWebView alloc] initWithFrame:frame configuration:cfg];
    self.web.navigationDelegate = self;
    self.web.UIDelegate = self;
    self.window.contentView = self.web;

    [self.window center];
    [self.window setFrameAutosaveName:@"BaltimoreEngineMainWindow"];
    [self.web loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:self.url]]];
    [self.window makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
}

- (BOOL)applicationShouldTerminateAfterLastWindowClosed:(NSApplication *)app {
    return YES;
}

- (void)reload:(id)sender {
    [self.web reload];
}

// Links to other hosts (dataset pages) open in the user's default browser.
- (void)webView:(WKWebView *)webView
    decidePolicyForNavigationAction:(WKNavigationAction *)action
                    decisionHandler:(void (^)(WKNavigationActionPolicy))handler {
    NSURL *u = action.request.URL;
    BOOL local = [u.host isEqualToString:@"127.0.0.1"] || [u.scheme isEqualToString:@"about"];
    if (!local && action.navigationType == WKNavigationTypeLinkActivated) {
        [[NSWorkspace sharedWorkspace] openURL:u];
        handler(WKNavigationActionPolicyCancel);
        return;
    }
    handler(WKNavigationActionPolicyAllow);
}

// target="_blank" links.
- (WKWebView *)webView:(WKWebView *)webView
    createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
               forNavigationAction:(WKNavigationAction *)action
                    windowFeatures:(WKWindowFeatures *)features {
    if (action.request.URL) {
        [[NSWorkspace sharedWorkspace] openURL:action.request.URL];
    }
    return nil;
}

// JavaScript alert() and confirm() as native sheets.
- (void)webView:(WKWebView *)webView
    runJavaScriptAlertPanelWithMessage:(NSString *)message
                      initiatedByFrame:(WKFrameInfo *)frame
                     completionHandler:(void (^)(void))done {
    NSAlert *a = [[NSAlert alloc] init];
    a.messageText = message;
    [a beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse r) { done(); }];
}

- (void)webView:(WKWebView *)webView
    runJavaScriptConfirmPanelWithMessage:(NSString *)message
                        initiatedByFrame:(WKFrameInfo *)frame
                       completionHandler:(void (^)(BOOL))done {
    NSAlert *a = [[NSAlert alloc] init];
    a.messageText = message;
    [a addButtonWithTitle:@"OK"];
    [a addButtonWithTitle:@"Cancel"];
    [a beginSheetModalForWindow:self.window completionHandler:^(NSModalResponse r) {
        done(r == NSAlertFirstButtonReturn);
    }];
}

@end

static NSMenuItem *item(NSString *title, SEL action, NSString *key) {
    return [[NSMenuItem alloc] initWithTitle:title action:action keyEquivalent:key];
}

static void buildMenus(NSString *name, AppDelegate *delegate) {
    NSMenu *bar = [[NSMenu alloc] init];

    NSMenuItem *appItem = [[NSMenuItem alloc] init];
    NSMenu *appMenu = [[NSMenu alloc] init];
    [appMenu addItem:item([@"About " stringByAppendingString:name], @selector(orderFrontStandardAboutPanel:), @"")];
    [appMenu addItem:[NSMenuItem separatorItem]];
    [appMenu addItem:item([@"Hide " stringByAppendingString:name], @selector(hide:), @"h")];
    NSMenuItem *others = item(@"Hide Others", @selector(hideOtherApplications:), @"h");
    others.keyEquivalentModifierMask = NSEventModifierFlagCommand | NSEventModifierFlagOption;
    [appMenu addItem:others];
    [appMenu addItem:item(@"Show All", @selector(unhideAllApplications:), @"")];
    [appMenu addItem:[NSMenuItem separatorItem]];
    [appMenu addItem:item([@"Quit " stringByAppendingString:name], @selector(terminate:), @"q")];
    appItem.submenu = appMenu;
    [bar addItem:appItem];

    NSMenuItem *editItem = [[NSMenuItem alloc] init];
    NSMenu *edit = [[NSMenu alloc] initWithTitle:@"Edit"];
    [edit addItem:item(@"Undo", @selector(undo:), @"z")];
    NSMenuItem *redo = item(@"Redo", @selector(redo:), @"z");
    redo.keyEquivalentModifierMask = NSEventModifierFlagCommand | NSEventModifierFlagShift;
    [edit addItem:redo];
    [edit addItem:[NSMenuItem separatorItem]];
    [edit addItem:item(@"Cut", @selector(cut:), @"x")];
    [edit addItem:item(@"Copy", @selector(copy:), @"c")];
    [edit addItem:item(@"Paste", @selector(paste:), @"v")];
    [edit addItem:item(@"Select All", @selector(selectAll:), @"a")];
    editItem.submenu = edit;
    [bar addItem:editItem];

    NSMenuItem *viewItem = [[NSMenuItem alloc] init];
    NSMenu *view = [[NSMenu alloc] initWithTitle:@"View"];
    NSMenuItem *reload = item(@"Reload", @selector(reload:), @"r");
    reload.target = delegate;
    [view addItem:reload];
    NSMenuItem *fs = item(@"Enter Full Screen", @selector(toggleFullScreen:), @"f");
    fs.keyEquivalentModifierMask = NSEventModifierFlagCommand | NSEventModifierFlagControl;
    [view addItem:fs];
    viewItem.submenu = view;
    [bar addItem:viewItem];

    NSMenuItem *winItem = [[NSMenuItem alloc] init];
    NSMenu *win = [[NSMenu alloc] initWithTitle:@"Window"];
    [win addItem:item(@"Minimize", @selector(performMiniaturize:), @"m")];
    [win addItem:item(@"Zoom", @selector(performZoom:), @"")];
    [win addItem:item(@"Close", @selector(performClose:), @"w")];
    winItem.submenu = win;
    [bar addItem:winItem];
    NSApp.windowsMenu = win;

    NSApp.mainMenu = bar;
}

void RunApp(const char *url, const char *title) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        AppDelegate *delegate = [[AppDelegate alloc] init];
        delegate.url = [NSString stringWithUTF8String:url];
        delegate.appTitle = [NSString stringWithUTF8String:title];
        buildMenus(delegate.appTitle, delegate);
        NSApp.delegate = delegate;
        [NSApp run];
    }
}
