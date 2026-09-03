//go:build darwin

package menubar

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/sshclient"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// restartBusyFloor is the minimum time the tray shows the disabled icon
// after a Restart click, so the feedback is visible even though the
// underlying Stop+Start cycle typically completes in microseconds.
const restartBusyFloor = 500 * time.Millisecond

// probeBudget bounds a whole forward-probe round. The probes run
// concurrently and each is capped at ProbeTimeout, so this is a backstop
// against a wedged resolver rather than a per-endpoint deadline.
const probeBudget = 2 * time.Second

// launchTimeout bounds the Chrome launch (profile mkdir plus fork/exec).
// Both are local, so this only trips if the filesystem is wedged — e.g. a
// stalled network home directory.
const launchTimeout = 10 * time.Second

// runBusy runs fn off the UI thread, holding the tray in its busy state
// until it returns. Every menu action that touches the filesystem, binds a
// listener, or waits on a goroutine goes through here: Wails dispatches
// OnClick on the UI thread, so doing that work inline freezes the menu bar
// for the duration.
//
// The busy flag both dims the icon and disables the actions that must not
// overlap, so a second click cannot race the first.
func (t *Tray) runBusy(label string, fn func() error) {
	if !t.busy.CompareAndSwap(false, true) {
		t.logger.Warn("busy", "action", label)

		return
	}

	// Reflect the busy state immediately; we are still on the UI thread.
	t.applyIcon(t.ctrl.Snapshot().State)
	t.rebuildMenu()

	go func() {
		start := time.Now()

		if err := fn(); err != nil {
			t.logger.Warn(label, "err", err)
		}

		if remaining := restartBusyFloor - time.Since(start); remaining > 0 {
			time.Sleep(remaining)
		}

		t.busy.Store(false)

		application.InvokeAsync(func() {
			t.applyIcon(t.ctrl.Snapshot().State)
			t.rebuildMenu()
		})
	}()
}

// Tray binds a ProxyController to a Wails v3 system tray.
type Tray struct {
	app     *application.App
	systray *application.SystemTray
	ctrl    *ProxyController
	logger  *log.Logger
	logPath string
	busy    atomic.Bool
	// autostart caches Autostart.IsEnabled() so rebuildMenu never makes
	// that call on the UI thread.
	autostart atomic.Bool
	// forwardUp caches the last ProbeForwards verdict (map[name]bool) so
	// rebuildMenu never dials on the UI thread. Refreshed off-thread when
	// the menu opens; nil until the first probe completes.
	forwardUp atomic.Pointer[map[string]bool]
	// probing guards against overlapping probes when the menu is opened
	// repeatedly in quick succession.
	probing atomic.Bool
}

// NewTray creates the system tray and wires it to the controller.
// The tray rebuilds its menu and icon whenever the controller's state changes.
func NewTray(app *application.App, ctrl *ProxyController, logger *log.Logger, logPath string) *Tray {
	t := &Tray{
		app:     app,
		systray: app.SystemTray.New(),
		ctrl:    ctrl,
		logger:  logger,
		logPath: logPath,
	}

	t.refreshAutostart()
	t.applyIcon(ctrl.Snapshot().State)
	t.rebuildMenu()

	ctrl.Subscribe(func(s Status) {
		application.InvokeAsync(func() {
			t.applyIcon(s.State)
			t.rebuildMenu()
		})
	})

	// Both click handlers open the menu themselves. Wails only falls back
	// to native menu tracking when the corresponding handler is nil (see
	// systrayPreClickCallback), so setting them is what gives us a
	// Go-side "menu is opening" signal at all — and the left click needs
	// the explicit OpenMenu it would otherwise have gotten natively.
	//
	// Probing dials the network, so it cannot happen inside rebuildMenu
	// on the UI thread. Kick it off here and rebuild once the verdicts
	// land: the menu opens immediately showing the previous verdicts,
	// then updates in place a few hundred milliseconds later.
	open := func() {
		t.rebuildMenu()
		t.systray.OpenMenu()
		t.refreshForwards()
	}

	t.systray.OnClick(open)
	t.systray.OnRightClick(open)

	return t
}

// refreshForwards probes the configured forwards off the UI thread and
// rebuilds the menu with the result. A probe already in flight wins; this
// call then returns immediately rather than queueing a duplicate.
func (t *Tray) refreshForwards() {
	if !t.probing.CompareAndSwap(false, true) {
		return
	}

	forwards := t.ctrl.Snapshot().Forwards
	if len(forwards) == 0 {
		t.probing.Store(false)

		return
	}

	go func() {
		defer t.probing.Store(false)

		ctx, cancel := context.WithTimeout(context.Background(), probeBudget)
		defer cancel()

		up := ProbeForwards(ctx, forwards)
		t.forwardUp.Store(&up)

		application.InvokeAsync(t.rebuildMenu)
	}()
}

// refreshAutostart re-reads the autostart state into the cache. Called at
// startup (before the event loop runs) and after a successful toggle, both
// of which are off the menu-build path.
func (t *Tray) refreshAutostart() {
	enabled, err := t.app.Autostart.IsEnabled()
	if err != nil {
		t.logger.Warn("autostart status", "err", err)

		return
	}

	t.autostart.Store(enabled)
}

func (t *Tray) applyIcon(s State) {
	var icon []byte
	if s == StateRunning && !t.busy.Load() {
		icon = iconRunning
	} else {
		icon = iconStopped
	}

	if runtime.GOOS == "darwin" {
		t.systray.SetTemplateIcon(icon)
	} else {
		t.systray.SetIcon(icon)
	}
}

func (t *Tray) rebuildMenu() {
	snap := t.ctrl.Snapshot()
	menu := t.app.NewMenu()

	menu.Add("🏄‍♂️ " + version).OnClick(func(_ *application.Context) {
		if err := openReleases(); err != nil {
			t.logger.Warn("open releases", "err", err)
		}
	})
	menu.AddSeparator()

	if snap.State == StateError && snap.LastError != nil {
		menu.Add(fmt.Sprintf("Error: %s", snap.LastError)).SetEnabled(false)
	}

	if snap.ListenAddr != "" {
		menu.Add("◉ " + snap.ListenAddr).SetEnabled(false)
	}

	// Tunnels: one glyph+name row each, click to start/stop that tunnel.
	if len(snap.Tunnels) > 0 {
		menu.AddSeparator()

		for _, ts := range snap.Tunnels {
			name := ts.Name
			listening := ts.State == TunnelListening

			glyph := "○"
			addr := "-"

			if listening {
				addr = ts.Addr

				if ts.ConnState == sshclient.ConnConnected {
					glyph = "◉"
				}
			}

			item := menu.Add(fmt.Sprintf("%s %s: %s", glyph, name, addr))
			item.SetEnabled(snap.State == StateRunning && !t.busy.Load())

			if listening {
				item.OnClick(func(_ *application.Context) {
					t.runBusy("stop tunnel "+name, func() error {
						return t.ctrl.StopTunnel(name)
					})
				})
			} else {
				item.OnClick(func(_ *application.Context) {
					t.runBusy("start tunnel "+name, func() error {
						return t.ctrl.StartTunnel(name)
					})
				})
			}
		}
	}

	// Forwards: one row per "forward" upstream, glyph reflecting whether
	// the configured endpoint currently accepts a connection. Unlike
	// tunnels these have nothing to start or stop, so the rows are
	// informational only.
	if len(snap.Forwards) > 0 {
		menu.AddSeparator()

		up := t.forwardUp.Load()

		for _, fs := range snap.Forwards {
			// "?" until the first probe returns, so an unprobed forward is
			// not misreported as down.
			glyph := "?"
			if up != nil {
				if reachable, ok := (*up)[fs.Name]; ok {
					glyph = "○"

					if reachable {
						glyph = "◉"
					}
				}
			}

			menu.Add(fmt.Sprintf("%s %s: %s", glyph, fs.Name, fs.Addr)).SetEnabled(false)
		}
	}

	// Isolated Chrome instance pointed at the local proxy.
	menu.AddSeparator()

	chrome := menu.Add("↗ Open Chrome")
	chrome.SetEnabled(snap.State == StateRunning && !t.busy.Load())
	chrome.OnClick(func(_ *application.Context) {
		t.runBusy("launch chrome", func() error {
			ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
			defer cancel()

			return LaunchChrome(ctx, snap.ListenAddr, snap.Chrome)
		})
	})

	// Actions grouped together: proxy lifecycle controls.
	menu.AddSeparator()

	if snap.State == StateRunning {
		stop := menu.Add("⏹︎ Stop")
		stop.SetEnabled(!t.busy.Load())
		stop.OnClick(func(_ *application.Context) {
			t.runBusy("stop", t.ctrl.Stop)
		})
	} else {
		start := menu.Add("▶︎ Start")
		start.SetEnabled(!t.busy.Load())
		start.OnClick(func(_ *application.Context) {
			t.runBusy("start", t.ctrl.Start)
		})
	}

	menu.Add("↺ Restart").OnClick(func(_ *application.Context) {
		t.runBusy("restart", t.ctrl.Restart)
	}).SetEnabled(snap.State == StateRunning && !t.busy.Load())

	// Read the cached value rather than calling Autostart.IsEnabled() here:
	// that is a synchronous SMAppService call, and rebuildMenu runs on the
	// UI thread on every controller notify. refreshAutostart keeps the
	// cache current off-thread.
	autostartEnabled := t.autostart.Load()

	loginGlyph := "□"
	if autostartEnabled {
		loginGlyph = "☑︎"
	}

	menu.Add(loginGlyph + " Start at Login").OnClick(func(_ *application.Context) {
		t.runBusy("toggle autostart", func() error {
			var err error
			if autostartEnabled {
				err = t.app.Autostart.Disable()
			} else {
				err = t.app.Autostart.Enable()
			}

			if err != nil {
				return err
			}

			t.refreshAutostart()

			return nil
		})
	})

	// Config path: click to reveal in Finder.
	menu.AddSeparator()

	// Reveal only forks `open` and never waits, so it cannot stall the UI
	// thread the way the lifecycle actions can — no runBusy needed.
	menu.Add("↗ Reveal logs in Finder").OnClick(func(_ *application.Context) {
		if err := RevealInFinder(context.Background(), t.logPath); err != nil {
			t.logger.Warn("reveal", "err", err)
		}
	})

	menu.Add("↗ Reveal config in Finder").OnClick(func(_ *application.Context) {
		if err := RevealInFinder(context.Background(), snap.ConfigPath); err != nil {
			t.logger.Warn("reveal", "err", err)
		}
	})

	// Quit isolated at the bottom.
	menu.AddSeparator()

	menu.Add("⏏︎ Quit").OnClick(func(_ *application.Context) {
		t.app.Quit()
	})

	t.systray.SetMenu(menu)
}
