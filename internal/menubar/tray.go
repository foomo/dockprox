//go:build safe

package menubar

import (
	"fmt"
	"runtime"

	"github.com/charmbracelet/log"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Tray binds a ProxyController to a Wails v3 system tray.
type Tray struct {
	app     *application.App
	systray *application.SystemTray
	ctrl    *ProxyController
	logger  *log.Logger
}

// NewTray creates the system tray and wires it to the controller.
// The tray rebuilds its menu and icon whenever the controller's state changes.
func NewTray(app *application.App, ctrl *ProxyController, logger *log.Logger) *Tray {
	t := &Tray{
		app:     app,
		systray: app.SystemTray.New(),
		ctrl:    ctrl,
		logger:  logger,
	}

	t.applyIcon(ctrl.Snapshot().State)
	t.rebuildMenu()
	ctrl.Subscribe(func(s Status) {
		t.applyIcon(s.State)
		t.rebuildMenu()
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

	menu.Add(fmt.Sprintf("● %s", snap.State)).SetEnabled(false)

	if snap.State == StateError && snap.LastError != nil {
		menu.Add(fmt.Sprintf("Error: %s", snap.LastError)).SetEnabled(false)
	}

	if snap.ListenAddr != "" {
		menu.Add("Listen: " + snap.ListenAddr).SetEnabled(false)
	}

	menu.Add("Config: " + abbreviateHome(snap.ConfigPath)).SetEnabled(false)

	menu.AddSeparator()

	menu.Add("Start").OnClick(func(_ *application.Context) {
		if err := t.ctrl.Start(); err != nil {
			t.logger.Warn("start", "err", err)
		}
	}).SetEnabled(snap.State == StateStopped || snap.State == StateError)

	menu.Add("Stop").OnClick(func(_ *application.Context) {
		_ = t.ctrl.Stop()
	}).SetEnabled(snap.State == StateRunning)

	menu.Add("Restart").OnClick(func(_ *application.Context) {
		if err := t.ctrl.Restart(); err != nil {
			t.logger.Warn("restart", "err", err)
		}
	}).SetEnabled(snap.State == StateRunning)

	menu.AddSeparator()

	menu.Add("Reveal Config in Finder").OnClick(func(_ *application.Context) {
		if err := RevealInFinder(snap.ConfigPath); err != nil {
			t.logger.Warn("reveal", "err", err)
		}
	})

	menu.AddSeparator()

	menu.Add("Quit dockprox").OnClick(func(_ *application.Context) {
		t.app.Quit()
	})

	t.systray.SetMenu(menu)
}

func abbreviateHome(p string) string {
	if home := homeDir(); home != "" && len(p) > len(home) && p[:len(home)] == home {
		return "~" + p[len(home):]
	}

	return p
}
