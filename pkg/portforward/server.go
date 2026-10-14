package portforward

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/upstream"
	"github.com/pkg/errors"
)

// Options configures a Server.
type Options struct {
	// Listen is the local address to bind (e.g. "127.0.0.1:5432").
	Listen string
	// Target is the "host:port" every connection is forwarded to.
	Target string
	// Dialer, when set, carries every connection. Otherwise Target's host
	// is routed through Matcher and Registry, falling back to direct.
	Dialer upstream.Dialer
	// Matcher maps hosts to upstream names. Required without Dialer.
	Matcher *match.Matcher
	// Registry resolves upstream names to Dialers. Required without Dialer.
	Registry *upstream.Registry
	// Logger receives structured log lines. Required.
	Logger *log.Logger
}

// Server is a TCP port forward. Serve blocks until the context is
// cancelled or the listener closes.
type Server struct {
	opts Options
	host string
	mu   sync.Mutex
	ln   net.Listener
}

// NewServer returns a Server bound to opts.Listen. The supplied context is
// used only for the listener handshake; Serve uses its own ctx.
func NewServer(ctx context.Context, opts Options) (*Server, error) {
	if opts.Logger == nil {
		return nil, errors.New("logger required")
	}

	host, _, err := net.SplitHostPort(opts.Target)
	if err != nil {
		return nil, errors.Wrap(err, "target")
	}

	if opts.Dialer == nil && (opts.Matcher == nil || opts.Registry == nil) {
		return nil, errors.New("dialer or matcher and registry required")
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", opts.Listen)
	if err != nil {
		return nil, errors.Wrap(err, "listen")
	}

	return &Server{opts: opts, host: host, ln: ln}, nil
}

// Addr returns the listener's resolved address (host:port).
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ln == nil {
		return ""
	}

	return s.ln.Addr().String()
}

// Close releases the listener. Idempotent.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ln == nil {
		return nil
	}

	return s.ln.Close()
}

// Serve accepts connections until ctx is cancelled or the listener closes.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()

	if ln == nil {
		return errors.New("listener closed")
	}

	go func() {
		<-ctx.Done()

		_ = s.Close()
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // graceful shutdown on ctx cancel
			}

			return errors.Wrap(err, "accept")
		}

		go s.handle(ctx, c)
	}
}

func (s *Server) handle(ctx context.Context, client net.Conn) {
	start := time.Now()

	defer client.Close()

	dialer := s.pick()

	up, err := dialer.Dial(ctx, s.opts.Target)
	if err != nil {
		s.opts.Logger.Warn("conn", "target", s.opts.Target, "upstream", dialer.Name(),
			"err", err.Error(), "dur", time.Since(start))

		return
	}
	defer up.Close()

	done := make(chan int64, 2)

	go func() { done <- pipe(up, client) }()
	go func() { done <- pipe(client, up) }()

	bytes := <-done + <-done

	s.opts.Logger.Info("conn", "target", s.opts.Target, "upstream", dialer.Name(),
		"bytes", bytes, "dur", time.Since(start))
}

func (s *Server) pick() upstream.Dialer {
	if s.opts.Dialer != nil {
		return s.opts.Dialer
	}

	if r, ok := s.opts.Matcher.First(s.host); ok {
		if d, ok := s.opts.Registry.Get(r.Upstream); ok {
			return d
		}
	}

	return s.opts.Registry.Direct()
}

// pipe copies src to dst, then half-closes dst so the far side sees EOF
// while its own direction keeps flowing — a client that shuts down writing
// after its request still gets the response.
func pipe(dst, src net.Conn) int64 {
	n, _ := io.Copy(dst, src)

	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	} else {
		_ = dst.Close()
	}

	return n
}
