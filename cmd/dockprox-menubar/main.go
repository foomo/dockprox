//go:build darwin

// Command dockprox-menubar runs the macOS menu bar (tray) app that drives a
// dockprox proxy in-process.
package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/internal/menubar"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func main() {
	cfgPath := flag.String("config", "", "YAML config file (default: auto-resolve)")

	flag.Parse()

	if err := run(*cfgPath); err != nil {
		log.Error("menubar exited with error", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	if cfgPath == "" {
		resolved, err := menubar.Resolve()
		if err != nil {
			return err
		}

		cfgPath = resolved
	}

	// Log setup must not depend on config validity: a bad config file is a
	// runtime condition surfaced via ctrl.Start() -> StateError, not a
	// reason to fail before the tray (and its log output) even exists.
	logPath, err := config.DefaultLogPath()
	if err != nil {
		return err
	}

	logWriter, err := config.OpenLogWriter(config.Defaults())
	if err != nil {
		return err
	}

	logger := log.NewWithOptions(logWriter, log.Options{ReportTimestamp: true})

	ctrl := menubar.New(cfgPath, logger)

	app := application.New(application.Options{
		Name: "Dockprox",
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	menubar.NewTray(app, ctrl, logger, logPath)

	// Start off the UI thread: it reads the config, validates ssh keys and
	// binds every listener, which would otherwise leave the menu bar
	// unresponsive for as long as that takes. The tray renders the
	// resulting state via its controller subscription.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			if err := ctrl.Start(); err != nil {
				logger.Warn("auto-start failed", "err", err)
			}
		}()
	})

	app.OnShutdown(func() { _ = ctrl.Stop() })

	sigCh := make(chan os.Signal, 1)

	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Info("signal received, quitting")
		app.Quit()
	}()

	return app.Run()
}
