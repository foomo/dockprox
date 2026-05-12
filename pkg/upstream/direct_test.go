package upstream_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/foomo/dockprox/pkg/upstream"
)

func TestDirectDialer_Dial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	d := upstream.NewDirect("direct")

	conn, err := d.Dial(context.Background(), net.JoinHostPort(host, port))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("GET / HTTP/1.0\r\nHost: " + host + "\r\n\r\n")); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 12)
	if _, err := conn.Read(buf); err != nil {
		t.Fatal(err)
	}

	if string(buf[:8]) != "HTTP/1.0" {
		t.Fatalf("unexpected response: %q", buf)
	}
}
