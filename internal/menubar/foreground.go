//go:build darwin

package menubar

/*
#cgo CFLAGS: -x objective-c -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#include <stdlib.h>

// activateApp asks for the app to come to the front: 0 = already active,
// 1 = via Launch Services, 2 = via NSApp. macOS 14+ ignores NSApp's
// self-activation without user input; Launch Services opening our own
// bundle is the path `open -b` takes. An unbundled binary has no bundle to
// open, so it falls back to NSApp.
static int activateApp(void) {
	if (NSApp.isActive) {
		return 0;
	}

	NSBundle *bundle = NSBundle.mainBundle;
	if (bundle.bundleIdentifier != nil) {
		NSWorkspaceOpenConfiguration *cfg = [NSWorkspaceOpenConfiguration configuration];
		cfg.activates = YES;

		[[NSWorkspace sharedWorkspace] openApplicationAtURL:bundle.bundleURL configuration:cfg completionHandler:nil];

		return 1;
	}

	[NSApp activateIgnoringOtherApps:YES];

	return 2;
}

// frontmostApp returns the bundle id (or name) of the frontmost app. The
// caller frees it.
static char* frontmostApp(void) {
	NSRunningApplication *app = [[NSWorkspace sharedWorkspace] frontmostApplication];
	NSString *id = app.bundleIdentifier ?: app.localizedName ?: @"";

	return strdup(id.UTF8String);
}
*/
import "C"

import (
	"sync"
	"time"
	"unsafe"

	"github.com/charmbracelet/log"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// activationWait bounds how long a sign request is held back while the app
// comes to the front. macOS may refuse the activation, so the request goes
// out regardless once this expires.
const activationWait = 500 * time.Millisecond

// foreground keeps the app frontmost while any agent sign request is
// pending. 1Password suppresses its approval prompt for apps that are not
// in front, and a menu bar (accessory) app never is on its own.
type foreground struct {
	app    *application.App
	logger *log.Logger
	active chan struct{}

	mu        sync.Mutex
	pending   int
	activated bool
}

func newForeground(app *application.App, logger *log.Logger) *foreground {
	f := &foreground{app: app, logger: logger, active: make(chan struct{}, 1)}

	app.Event.OnApplicationEvent(events.Mac.ApplicationDidBecomeActive, func(*application.ApplicationEvent) {
		select {
		case f.active <- struct{}{}:
		default:
		}
	})

	return f
}

// set is the controller's OnApproval hook. The first pending request
// activates the app and waits for it to become active, so the agent sees it
// in front; once none are pending, the app hides, which hands focus back to
// the app that had it.
//
// It runs on the dialing goroutine. The main thread is only reached via
// InvokeAsync and every wait is bounded, so a busy UI thread delays the
// sign request by at most activationWait instead of deadlocking the dial.
func (f *foreground) set(pending bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if pending {
		f.pending++
		if f.pending == 1 {
			f.activate()
		}

		return
	}

	if f.pending > 0 {
		f.pending--
	}

	if f.pending == 0 && f.activated {
		f.activated = false

		f.logger.Debug("foreground hide")
		application.InvokeAsync(f.app.Hide)
	}
}

func (f *foreground) activate() {
	// Drop a signal left over from an earlier activation.
	select {
	case <-f.active:
	default:
	}

	start := time.Now()
	res := make(chan int, 1)

	application.InvokeAsync(func() { res <- int(C.activateApp()) })

	timer := time.NewTimer(activationWait)
	defer timer.Stop()

	var via int

	select {
	case via = <-res:
	case <-timer.C:
		f.logger.Debug("foreground activate: main thread busy", "dur", time.Since(start))

		return
	}

	if via == 0 {
		f.logger.Debug("foreground activate: already active")

		return
	}

	f.activated = true

	became := true

	select {
	case <-f.active:
	case <-timer.C:
		became = false
	}

	f.logger.Debug("foreground activate", "via", via, "became", became, "dur", time.Since(start), "frontmost", frontmost())
}

// frontmost reports which app macOS considers frontmost, for the log.
// NSWorkspace is safe off the main thread.
func frontmost() string {
	cs := C.frontmostApp()
	defer C.free(unsafe.Pointer(cs))

	return C.GoString(cs)
}
