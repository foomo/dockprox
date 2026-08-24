package socks5server_test

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/socks5server"
	"github.com/foomo/dockprox/pkg/upstream"
)

func setup(t *testing.T, cfg *config.Config) (string, func()) {
	t.Helper()

	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	reg, err := upstream.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}

	known := map[string]struct{}{}
	for n := range cfg.Upstreams {
		known[n] = struct{}{}
	}

	rules := make([]match.Rule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		rules = append(rules, match.Rule{Pattern: r.Match, Upstream: r.Upstream})
	}

	m, err := match.New(rules, known)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	srv, err := socks5server.NewServer(ctx, socks5server.Options{
		Listen:   "127.0.0.1:0",
		Matcher:  m,
		Registry: reg,
		Logger:   log.New(io.Discard),
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	go func() { _ = srv.Serve(ctx) }()

	for srv.Addr() == "" {
		time.Sleep(5 * time.Millisecond)
	}

	return srv.Addr(), func() { cancel(); _ = srv.Close() }
}

// handshakeNoAuth performs the RFC1928 greeting and returns the selected
// method byte.
func handshakeNoAuth(t *testing.T, c net.Conn) byte {
	t.Helper()

	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}

	resp := make([]byte, 2)
	if _, err := io.ReadFull(c, resp); err != nil {
		t.Fatal(err)
	}

	if resp[0] != 0x05 {
		t.Fatalf("unexpected version %d", resp[0])
	}

	return resp[1]
}

func connect(t *testing.T, c net.Conn, host string, port int) byte {
	t.Helper()

	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)

	pb := make([]byte, 2)
	binary.BigEndian.PutUint16(pb, uint16(port))
	req = append(req, pb...)

	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil {
		t.Fatal(err)
	}

	switch head[3] {
	case 0x01:
		_, _ = io.ReadFull(c, make([]byte, 6))
	case 0x03:
		n := make([]byte, 1)
		_, _ = io.ReadFull(c, n)
		_, _ = io.ReadFull(c, make([]byte, int(n[0])+2))
	case 0x04:
		_, _ = io.ReadFull(c, make([]byte, 18))
	}

	return head[1]
}

func TestSocks5Server_Connect_Direct(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	addr, stop := setup(t, &config.Config{Listen: "127.0.0.1:0", LogLevel: "info"})
	defer stop()

	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if method := handshakeNoAuth(t, c); method != 0x00 {
		t.Fatalf("method=%d, want 0x00", method)
	}

	host, portStr, _ := net.SplitHostPort(backend.Listener.Addr().String())

	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	if reply := connect(t, c, host, port); reply != 0x00 {
		t.Fatalf("connect reply=%d, want 0x00", reply)
	}

	if _, err := c.Write([]byte("GET / HTTP/1.0\r\nHost: " + host + "\r\n\r\n")); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 64)

	n, _ := c.Read(buf)
	if got := string(buf[:n]); len(got) == 0 {
		t.Fatalf("empty response")
	}
}

func TestSocks5Server_RejectsAuthRequired(t *testing.T) {
	addr, stop := setup(t, &config.Config{Listen: "127.0.0.1:0", LogLevel: "info"})
	defer stop()

	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Offer only username/password auth (0x02), which the server does not support.
	if _, err := c.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatal(err)
	}

	resp := make([]byte, 2)
	if _, err := io.ReadFull(c, resp); err != nil {
		t.Fatal(err)
	}

	if resp[1] != 0xFF {
		t.Fatalf("method=%d, want 0xFF (no acceptable methods)", resp[1])
	}
}

func TestSocks5Server_RejectsUnsupportedCommand(t *testing.T) {
	addr, stop := setup(t, &config.Config{Listen: "127.0.0.1:0", LogLevel: "info"})
	defer stop()

	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	handshakeNoAuth(t, c)

	// BIND command (0x02) instead of CONNECT (0x01).
	req := []byte{0x05, 0x02, 0x00, 0x03, 9}
	req = append(req, "localhost"...)
	req = append(req, 0x00, 0x50)

	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil {
		t.Fatal(err)
	}

	if head[1] != 0x07 {
		t.Fatalf("reply=%d, want 0x07 (command not supported)", head[1])
	}
}

func TestSocks5Server_UnreachableHost(t *testing.T) {
	addr, stop := setup(t, &config.Config{Listen: "127.0.0.1:0", LogLevel: "info"})
	defer stop()

	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	handshakeNoAuth(t, c)

	if reply := connect(t, c, "127.0.0.1", 1); reply == 0x00 {
		t.Fatalf("expected connect failure for closed port")
	}
}

// countingDialer is a fixed upstream.Dialer that records how many times it
// was used and dials a fixed backend regardless of the requested host.
type countingDialer struct {
	backend string
	mu      sync.Mutex
	n       int
}

func (d *countingDialer) Name() string { return "fixed" }

func (d *countingDialer) Dial(ctx context.Context, _ string) (net.Conn, error) {
	d.mu.Lock()
	d.n++
	d.mu.Unlock()

	return (&net.Dialer{}).DialContext(ctx, "tcp", d.backend)
}

func (d *countingDialer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.n
}

func TestSocks5Server_FixedDialer(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	dialer := &countingDialer{backend: backend.Listener.Addr().String()}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// No Matcher, no Registry: a fixed Dialer replaces both.
	srv, err := socks5server.NewServer(ctx, socks5server.Options{
		Listen: "127.0.0.1:0",
		Dialer: dialer,
		Logger: log.New(io.Discard),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	go func() { _ = srv.Serve(ctx) }()

	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	handshakeNoAuth(t, c)

	// A host the dialer ignores entirely — it always reaches the backend.
	if reply := connect(t, c, "anything.invalid", 443); reply != 0x00 {
		t.Fatalf("connect reply=%d, want 0x00", reply)
	}

	if got := dialer.count(); got != 1 {
		t.Fatalf("dialer used %d times, want 1", got)
	}
}

func TestNewServer_RequiresMatcherWithoutDialer(t *testing.T) {
	_, err := socks5server.NewServer(t.Context(), socks5server.Options{
		Listen: "127.0.0.1:0",
		Logger: log.New(io.Discard),
	})
	if err == nil || !strings.Contains(err.Error(), "matcher") {
		t.Fatalf("err=%v want substring %q", err, "matcher")
	}
}
