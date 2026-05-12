// Command dockprox is an inverse HTTP(S) proxy with SOCKS5 support.
//
// By default it dials destinations directly. Only hosts matched by a rule
// in the configuration file are routed through a named upstream proxy
// (SOCKS5 or HTTP CONNECT). See https://github.com/foomo/dockprox.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/foomo/dockprox/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Execute(ctx); err != nil {
		return 1
	}

	return 0
}
