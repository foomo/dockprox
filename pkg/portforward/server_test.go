package portforward_test

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/portforward"
	"github.com/foomo/dockprox/pkg/upstream"
)

// echoServer echoes every connection until the client half-closes.
func echoServer(t *testing.T) string {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				defer c.Close()

				_, _ = io.Copy(c, c)
			}()
		}
	}()

	return ln.Addr().String()
}

// recordingDialer dials directly and records the requested target.
type recordingDialer struct{ got chan string }

func (d *recordingDialer) Name() string { return "rec" }

func (d *recordingDialer) Dial(ctx context.Context, hostPort string) (net.Conn, error) {
	d.got <- hostPort

	return (&net.Dialer{}).DialContext(ctx, "tcp", hostPort)
}

func serve(t *testing.T, opts portforward.Options) string {
	t.Helper()

	opts.Listen = "127.0.0.1:0"
	opts.Logger = log.New(io.Discard)

	srv, err := portforward.NewServer(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}

	go func() { _ = srv.Serve(t.Context()) }()

	return srv.Addr()
}

// roundTrip writes msg, half-closes, and returns everything read back.
func roundTrip(t *testing.T, addr, msg string) string {
	t.Helper()

	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}

	tc, ok := c.(*net.TCPConn)
	if !ok {
		t.Fatalf("conn is %T, want *net.TCPConn", c)
	}

	_ = tc.CloseWrite()

	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatal(err)
	}

	return string(got)
}

func TestServer_PinnedDialerForwardsToTarget(t *testing.T) {
	target := echoServer(t)
	d := &recordingDialer{got: make(chan string, 1)}

	addr := serve(t, portforward.Options{Target: target, Dialer: d})

	// The response arrives although the client half-closed first.
	if got := roundTrip(t, addr, "ping"); got != "ping" {
		t.Fatalf("echo = %q, want %q", got, "ping")
	}

	if got := <-d.got; got != target {
		t.Fatalf("dialed %q, want %q", got, target)
	}
}

func TestServer_RoutesTargetHostByRules(t *testing.T) {
	target := echoServer(t)
	_, port, _ := net.SplitHostPort(target)

	// db.routed.test matches a rule pointing at a forward upstream that lands on
	// the echo server, proving the target's host went through the matcher.
	cfg := &config.Config{Upstreams: map[string]config.Upstream{
		"fwd": {Type: config.UpstreamForward, Addr: target},
	}}

	reg, err := upstream.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m, err := match.New([]match.Rule{{Pattern: "db.routed.test", Upstream: "fwd"}}, map[string]struct{}{"fwd": {}})
	if err != nil {
		t.Fatal(err)
	}

	addr := serve(t, portforward.Options{Target: "db.routed.test:" + port, Matcher: m, Registry: reg})

	if got := roundTrip(t, addr, "via rules"); got != "via rules" {
		t.Fatalf("echo = %q", got)
	}
}
