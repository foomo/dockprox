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

// Tray binds a ProxyController to a Wails v3 system tray.
type Tray struct {
	app     *application.App
	systray *application.SystemTray
	ctrl    *ProxyController
	logger  *log.Logger
	logPath string
	busy    atomic.Bool
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

	t.applyIcon(ctrl.Snapshot().State)
	t.rebuildMenu()

	ctrl.Subscribe(func(s Status) {
		application.InvokeAsync(func() {
			t.applyIcon(s.State)
			t.rebuildMenu()
		})
	})

	t.systray.OnRightClick(func() {
		t.rebuildMenu()
		t.systray.OpenMenu()
	})

	return t
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
					if err := t.ctrl.StopTunnel(name); err != nil {
						t.logger.Warn("stop tunnel", "name", name, "err", err)
					}
				})
			} else {
				item.OnClick(func(_ *application.Context) {
					if err := t.ctrl.StartTunnel(name); err != nil {
						t.logger.Warn("start tunnel", "name", name, "err", err)
					}
				})
			}
		}
	}

	// Actions grouped together: proxy lifecycle controls.
	menu.AddSeparator()

	if snap.State == StateRunning {
		menu.Add("⏹︎ Stop").OnClick(func(_ *application.Context) {
			_ = t.ctrl.Stop()
		})
	} else {
		menu.Add("▶︎ Start").OnClick(func(_ *application.Context) {
			if err := t.ctrl.Start(); err != nil {
				t.logger.Warn("start", "err", err)
			}
		})
	}

	menu.Add("↺ Restart").OnClick(func(_ *application.Context) {
		t.busy.Store(true)
		t.applyIcon(snap.State)
		t.rebuildMenu()

		go func() {
			start := time.Now()

			if err := t.ctrl.Restart(); err != nil {
				t.logger.Warn("restart", "err", err)
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
	}).SetEnabled(snap.State == StateRunning && !t.busy.Load())

	autostartEnabled, err := t.app.Autostart.IsEnabled()
	if err != nil {
		t.logger.Warn("autostart status", "err", err)
	}

	loginGlyph := "□"
	if autostartEnabled {
		loginGlyph = "☑︎"
	}

	menu.Add(loginGlyph + " Start at Login").OnClick(func(_ *application.Context) {
		var err error
		if autostartEnabled {
			err = t.app.Autostart.Disable()
		} else {
			err = t.app.Autostart.Enable()
		}

		if err != nil {
			t.logger.Warn("toggle autostart", "err", err)
		}

		t.rebuildMenu()
	})

	// Config path: click to reveal in Finder.
	menu.AddSeparator()

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
