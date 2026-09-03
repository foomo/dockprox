//go:build darwin

package menubar

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/pkg/errors"
)

// defaultChromeApp is the app name handed to `open -a`. LaunchServices
// resolves it wherever Chrome is installed, so no absolute path is baked in.
const defaultChromeApp = "Google Chrome"

// chromeProfileSubpath is the profile directory under the user's
// Application Support dir, keeping this instance's cookies, logins and
// extensions out of the user's own Chrome profile.
const chromeProfileSubpath = "dockprox/chrome"

// ChromeProfileDir returns the profile directory for the isolated Chrome
// instance: ~/Library/Application Support/dockprox/chrome on macOS.
func ChromeProfileDir() string {
	return filepath.Join(homeDir(), "Library", "Application Support", chromeProfileSubpath)
}

// LaunchChrome starts an isolated Chrome instance whose traffic goes
// through the proxy listening on listenAddr. Returns nil once the launch
// has been started (it does not wait for Chrome to exit).
//
// The launch goes through `open`, not a direct exec of Chrome's inner
// binary. Exec'ing a path inside another app's bundle makes macOS gate the
// call behind the App Management permission
// (kTCCServiceSystemPolicyAppBundles), which prompts the user with
// "dockprox needs permission to update or delete other applications" —
// alarming, and re-prompted on every rebuild because the app is only
// ad-hoc signed. Handing the request to LaunchServices instead means
// dockprox never touches the bundle, so no permission is involved. This is
// the same reason RevealInFinder shells out to `open -R`.
//
// ctx bounds the launch itself — the profile mkdir and running `open` — and
// is deliberately not passed to the command: exec.CommandContext would
// kill the process when ctx expired. `open` exits as soon as it has handed
// off, so that would not kill Chrome itself, but it would still abort the
// handoff mid-flight.
func LaunchChrome(ctx context.Context, listenAddr string, cfg *config.Chrome) error {
	if listenAddr == "" {
		return errors.New("proxy not listening")
	}

	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, "launch chrome")
	}

	dir := ChromeProfileDir()

	// MkdirAll has no ctx-aware form; run it where the deadline can still
	// win the race, so a wedged filesystem surfaces as a timeout instead of
	// an indefinite hang. The goroutine is abandoned on timeout, not
	// leaked indefinitely: it ends whenever the filesystem call returns.
	errc := make(chan error, 1)

	go func() { errc <- os.MkdirAll(dir, 0o700) }()

	select {
	case err := <-errc:
		if err != nil {
			return errors.Wrap(err, "mkdir chrome profile")
		}
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "mkdir chrome profile")
	}

	// Unlike a direct exec, `open` resolves the app and returns quickly, so
	// wait for it: its exit status is the only signal that the app name or
	// bundle ID was wrong. Chrome keeps running after `open` exits.
	cmd := buildChromeCommand(listenAddr, dir, cfg)

	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return errors.Wrapf(err, "open chrome: %s", msg)
		}

		return errors.Wrap(err, "open chrome")
	}

	return nil
}

// buildChromeCommand assembles the `open` invocation. Extra flags from the
// config are appended last so they can override the defaults.
//
//	open -n -a "Google Chrome" --args --proxy-server=... --user-data-dir=...
//
// -n forces a separate instance: without it LaunchServices just activates
// any already-running Chrome and drops the arguments, silently ignoring the
// proxy and the isolated profile. Everything after --args goes to Chrome's
// argv untouched.
func buildChromeCommand(
	listenAddr, profileDir string,
	cfg *config.Chrome,
) *exec.Cmd {
	app := defaultChromeApp
	if cfg != nil && cfg.App != "" {
		app = cfg.App
	}

	// A bundle ID needs -b; -a takes an app name or a path. Telling them
	// apart by the dotted-no-separator shape keeps "Google Chrome" and
	// "/Applications/Chromium.app" on -a where they belong.
	selector := "-a"
	if isBundleID(app) {
		selector = "-b"
	}

	args := []string{"-n", selector, app, "--args",
		"--window-name=dockprox",
		"--proxy-server=http://" + listenAddr,
		// Chrome bypasses the proxy for these by default; route them too,
		// so rules matching a *.local.gd-style host are not skipped.
		"--proxy-bypass-list=<-loopback>",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
	}

	if cfg != nil {
		args = append(args, cfg.Flags...)
	}

	return exec.Command("open", args...)
}

// isBundleID reports whether s looks like a reverse-DNS bundle identifier
// (e.g. com.google.Chrome) rather than an app name or filesystem path.
func isBundleID(s string) bool {
	return strings.Contains(s, ".") &&
		!strings.Contains(s, "/") &&
		!strings.Contains(s, " ") &&
		!strings.HasSuffix(s, ".app")
}
