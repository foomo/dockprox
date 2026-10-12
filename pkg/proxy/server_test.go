package proxy_test

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/proxy"
	"github.com/foomo/dockprox/pkg/upstream"
)

func ExampleServer() {
	cfg := &config.Config{Listen: "127.0.0.1:0", LogLevel: "info"}
	_ = cfg.Validate()
	reg, _ := upstream.NewRegistry(cfg)
	m, _ := match.New(nil, map[string]struct{}{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	srv, _ := proxy.NewServer(ctx, proxy.Options{
		Listen: "127.0.0.1:0", Matcher: m, Registry: reg,
		Logger: log.New(io.Discard),
	})

	_ = srv.Serve(ctx)

	fmt.Println("served")
	// Output: served
}
