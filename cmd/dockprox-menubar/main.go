//go:build safe

package main

import (
	"os"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/internal/menubar"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func main() {
	logger := log.NewWithOptions(os.Stderr, log.Options{ReportTimestamp: true})

	cfgPath, err := menubar.Resolve()
	if err != nil {
		logger.Fatal("config resolve", "err", err)
	}

	ctrl := menubar.New(cfgPath, logger)

	app := application.New(application.Options{
		Name: "dockprox",
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	menubar.NewTray(app, ctrl, logger)

	if err := ctrl.Start(); err != nil {
		logger.Warn("auto-start failed", "err", err)
	}

	app.OnShutdown(func() { _ = ctrl.Stop() })

	if err := app.Run(); err != nil {
		logger.Fatal("app run", "err", err)
	}
}
