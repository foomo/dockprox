package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/foomo/dockprox/internal/cli"
	"github.com/foomo/dockprox/pkg/secret"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Config is the user's own file, so cmd: references are trusted.
	secret.SetDefault(secret.New(secret.WithCmd()))

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Execute(ctx); err != nil {
		return 1
	}

	return 0
}
