package proxy_test

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/proxy"
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

	srv, err := proxy.NewServer(ctx, proxy.Options{
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

func TestProxy_Bypass_HTTPS(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	addr, stop := setup(t, &config.Config{Listen: "127.0.0.1:0", LogLevel: "info"})
	defer stop()

	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(&url.URL{Scheme: "http", Host: addr}),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, backend.URL, nil)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body=%q", body)
	}
}

func TestProxy_Forward_HTTP(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("plain"))
	}))
	defer backend.Close()

	addr, stop := setup(t, &config.Config{Listen: "127.0.0.1:0", LogLevel: "info"})
	defer stop()

	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: addr}),
	}}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, backend.URL, nil)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "plain" {
		t.Fatalf("body=%q", body)
	}
}

func TestProxy_MalformedCONNECT(t *testing.T) {
	addr, stop := setup(t, &config.Config{Listen: "127.0.0.1:0", LogLevel: "info"})
	defer stop()

	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	_, _ = c.Write([]byte("GARBAGE\r\n\r\n"))
	buf := make([]byte, 64)

	n, _ := c.Read(buf)
	if !strings.Contains(string(buf[:n]), "400") && !strings.Contains(string(buf[:n]), "405") {
		t.Fatalf("expected 4xx, got %q", buf[:n])
	}
}
