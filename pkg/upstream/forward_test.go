package upstream_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foomo/dockprox/pkg/upstream"
)

func TestForwardDialer_IgnoresRequestedTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	d := upstream.NewForward("cluster-a", srv.Listener.Addr().String())

	// The requested target is a host that does not resolve and a port
	// nothing listens on; the dialer must ignore it and reach srv anyway.
	conn, err := d.Dial(context.Background(), "app.a.local.gd:443")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("GET / HTTP/1.0\r\nHost: app.a.local.gd\r\n\r\n")); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 64)

	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(buf[:n]), "HTTP/1.0 200") {
		t.Fatalf("unexpected response: %q", buf[:n])
	}
}

func TestForwardDialer_Name(t *testing.T) {
	if got := upstream.NewForward("cluster-a", "127.0.0.1:1").Name(); got != "cluster-a" {
		t.Fatalf("Name() = %q, want %q", got, "cluster-a")
	}
}

func TestForwardDialer_UnreachableAddr(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := ln.Addr().String()
	_ = ln.Close()

	if _, err := upstream.NewForward("dead", addr).Dial(t.Context(), "example.com:443"); err == nil {
		t.Fatal("expected error dialing closed port")
	}
}
