package upstream_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/upstream"
)

func newFakeHTTPConnect(t *testing.T) string {
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

			go func(c net.Conn) {
				defer c.Close()

				br := bufio.NewReader(c)

				req, err := http.ReadRequest(br)
				if err != nil || req.Method != http.MethodConnect {
					_, _ = c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
					return
				}

				bk, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", req.Host)
				if err != nil {
					_, _ = c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
					return
				}
				defer bk.Close()

				_, _ = c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				done := make(chan struct{}, 1)

				go func() { _, _ = io.Copy(bk, br); done <- struct{}{} }()

				_, _ = io.Copy(c, bk)

				<-done
			}(c)
		}
	}()

	return ln.Addr().String()
}

func TestHTTPDialer_Dial(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	proxyAddr := newFakeHTTPConnect(t)

	d, err := upstream.NewHTTP("corp", config.Upstream{
		Type: config.UpstreamHTTP,
		URL:  "http://" + proxyAddr,
	})
	if err != nil {
		t.Fatal(err)
	}

	conn, err := d.Dial(context.Background(), backend.Listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	host, _, _ := net.SplitHostPort(backend.Listener.Addr().String())
	_, _ = conn.Write([]byte("GET / HTTP/1.0\r\nHost: " + host + "\r\n\r\n"))
	buf := make([]byte, 64)

	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "HTTP/1.0 200") {
		t.Fatalf("unexpected: %q", buf[:n])
	}
}
