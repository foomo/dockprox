//go:build darwin

package menubar

import (
	"context"
	"fmt"
	"runtime"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/sshclient"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Tray binds a ProxyController to a Wails v3 system tray.
type Tray struct {
	app     *application.App
	systray *application.SystemTray
	ctrl    *ProxyController
	logger  *log.Logger
	logPath string
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
	if s == StateRunning {
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

	if snap.State == StateError && snap.LastError != nil {
		menu.Add(fmt.Sprintf("Error: %s", snap.LastError)).SetEnabled(false)
	}

	if snap.ListenAddr != "" {
		menu.Add("◉ " + snap.ListenAddr).SetEnabled(false)
	}

	// Tunnels: read-only status list, one glyph+name row each.
	if len(snap.Tunnels) > 0 {
		menu.AddSeparator()

		for _, ts := range snap.Tunnels {
			glyph := "○"
			addr := "-"

			if ts.ConnState == sshclient.ConnConnected {
				glyph = "◉"
				addr = ts.Addr
			}

			menu.Add(fmt.Sprintf("%s %s: %s", glyph, ts.Name, addr)).SetEnabled(false)
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
		if err := t.ctrl.Restart(); err != nil {
			t.logger.Warn("restart", "err", err)
		}
	}).SetEnabled(snap.State == StateRunning)

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
