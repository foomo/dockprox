//go:build darwin

package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/internal/menubar"
	"github.com/spf13/cobra"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func newMenubarCmd() *cobra.Command {
	var cfgPath string

	cmd := &cobra.Command{
		Use:   "menubar",
		Short: "Run the macOS menu bar app",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMenubar(cmd, cfgPath)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "YAML config file (default: auto-resolve)")

	return cmd
}

func runMenubar(cmd *cobra.Command, cfgPath string) error {
	logger := log.NewWithOptions(cmd.ErrOrStderr(), log.Options{ReportTimestamp: true})

	if cfgPath == "" {
		resolved, err := menubar.Resolve()
		if err != nil {
			return err
		}

		cfgPath = resolved
	}

	ctrl := menubar.New(cfgPath, logger)

	app := application.New(application.Options{
		Name: "dockprox",
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
	})

	menubar.NewTray(app, ctrl, logger)

	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if err := ctrl.Start(); err != nil {
			logger.Warn("auto-start failed", "err", err)
		}
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
