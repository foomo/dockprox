package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/foomo/dockprox/pkg/upstream"
)

func (s *Server) handle(ctx context.Context, client net.Conn) {
	start := time.Now()

	defer client.Close()

	br := bufio.NewReader(client)

	peek, err := br.Peek(8)
	if err != nil {
		return
	}

	method := strings.SplitN(string(peek), " ", 2)[0]
	switch method {
	case http.MethodConnect:
		s.handleConnect(ctx, client, br, start)
	default:
		s.handleForward(ctx, client, br, start)
	}
}

func (s *Server) handleConnect(ctx context.Context, client net.Conn, br *bufio.Reader, start time.Time) {
	req, err := http.ReadRequest(br)
	if err != nil {
		_, _ = fmt.Fprintf(client, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}

	target := req.RequestURI

	host, _, err := net.SplitHostPort(target)
	if err != nil {
		_, _ = fmt.Fprintf(client, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}

	dialer := s.pick(host)

	up, err := dialer.Dial(ctx, target)
	if err != nil {
		s.opts.Logger.Warn("conn", "host", host, "upstream", dialer.Name(),
			"err", err.Error(), "dur", time.Since(start))

		_, _ = fmt.Fprintf(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n")

		return
	}
	defer up.Close()

	_, _ = fmt.Fprintf(client, "HTTP/1.1 200 Connection Established\r\n\r\n")

	bytes := spliceClientServer(client, br, up)
	s.opts.Logger.Info("conn", "host", host, "upstream", dialer.Name(),
		"method", http.MethodConnect, "status", 200, "bytes", bytes,
		"dur", time.Since(start))
}

func (s *Server) handleForward(ctx context.Context, client net.Conn, br *bufio.Reader, start time.Time) {
	req, err := http.ReadRequest(br)
	if err != nil {
		_, _ = fmt.Fprintf(client, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}

	if req.URL == nil || req.URL.Host == "" {
		_, _ = fmt.Fprintf(client, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}

	host := req.URL.Hostname()

	port := req.URL.Port()
	if port == "" {
		port = "80"
	}

	dialer := s.pick(host)

	up, err := dialer.Dial(ctx, net.JoinHostPort(host, port))
	if err != nil {
		_, _ = fmt.Fprintf(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n")

		s.opts.Logger.Warn("conn", "host", host, "upstream", dialer.Name(),
			"err", err.Error(), "dur", time.Since(start))

		return
	}
	defer up.Close()

	req.RequestURI = ""
	req.URL.Scheme = ""

	req.URL.Host = ""
	if err := req.Write(up); err != nil {
		s.opts.Logger.Warn("conn", "host", host, "upstream", dialer.Name(),
			"err", "write upstream: "+err.Error(), "dur", time.Since(start))

		return
	}

	resp, err := http.ReadResponse(bufio.NewReader(up), req)
	if err != nil {
		s.opts.Logger.Warn("conn", "host", host, "upstream", dialer.Name(),
			"err", "read upstream: "+err.Error(), "dur", time.Since(start))

		return
	}
	defer resp.Body.Close()

	if err := resp.Write(client); err != nil {
		s.opts.Logger.Warn("conn", "host", host, "upstream", dialer.Name(),
			"err", "write client: "+err.Error(), "dur", time.Since(start))

		return
	}

	s.opts.Logger.Info("conn", "host", host, "upstream", dialer.Name(),
		"method", req.Method, "status", resp.StatusCode,
		"dur", time.Since(start))
}

func (s *Server) pick(host string) upstream.Dialer {
	if r, ok := s.opts.Matcher.First(host); ok {
		if d, ok := s.opts.Registry.Get(r.Upstream); ok {
			return d
		}
	}

	return s.opts.Registry.Direct()
}

func spliceClientServer(client net.Conn, br *bufio.Reader, up net.Conn) int64 {
	done := make(chan int64, 2)

	go func() {
		// drain any bytes already buffered in br first, then pump client.
		n1, _ := io.Copy(up, br)

		n2, _ := io.Copy(up, client)
		done <- n1 + n2
	}()
	go func() {
		n, _ := io.Copy(client, up)
		done <- n
	}()

	a := <-done
	b := <-done

	return a + b
}
