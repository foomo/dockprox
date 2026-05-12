package upstream_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/upstream"
)

func TestSocks5Dialer_NoAuth(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	proxy := newFakeSocks5(t, backend.Listener.Addr().String())

	d := upstream.NewSocks5("jump", config.Upstream{
		Type: config.UpstreamSocks5,
		Addr: proxy.addr,
		DNS:  "remote",
	})

	conn, err := d.Dial(context.Background(), backend.Listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	host, _, _ := net.SplitHostPort(backend.Listener.Addr().String())
	if _, err := conn.Write([]byte("GET / HTTP/1.0\r\nHost: " + host + "\r\n\r\n")); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 64)

	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "HTTP/1.0 200") {
		t.Fatalf("unexpected: %q", buf[:n])
	}
}

func TestSocks5Dialer_Auth_OK(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer backend.Close()

	proxy := newFakeSocks5Auth(t, backend.Listener.Addr().String(), "u", "p")
	d := upstream.NewSocks5("jump", config.Upstream{
		Type: config.UpstreamSocks5,
		Addr: proxy.addr,
		Auth: &config.UpstreamAuth{Username: "u", Password: "p"},
	})

	conn, err := d.Dial(context.Background(), backend.Listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	conn.Close()
}

func TestSocks5Dialer_Auth_Bad(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer backend.Close()

	proxy := newFakeSocks5Auth(t, backend.Listener.Addr().String(), "u", "p")

	d := upstream.NewSocks5("jump", config.Upstream{
		Type: config.UpstreamSocks5,
		Addr: proxy.addr,
		Auth: &config.UpstreamAuth{Username: "wrong", Password: "p"},
	})
	if _, err := d.Dial(context.Background(), backend.Listener.Addr().String()); err == nil {
		t.Fatal("expected auth failure")
	}
}
