// Package socks5server implements a minimal inbound SOCKS5 server (RFC
// 1928). CONNECT requests are routed either through the Matcher/Registry
// used by pkg/proxy, or — when Options.Dialer is set — through that single
// dialer, which is how ssh tunnel listeners are built. Only the no-auth
// method and the CONNECT command are supported.
package socks5server

import (
	"context"
	"net"
	"sync"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/upstream"
	"github.com/pkg/errors"
)

// Options configures a Server.
type Options struct {
	// Listen is the address the server binds to (e.g. "127.0.0.1:1080").
	Listen string
	// Matcher maps hosts to upstream names.
	Matcher *match.Matcher
	// Registry resolves upstream names to Dialers.
	Registry *upstream.Registry
	// Dialer, when set, handles every connection regardless of host. This
	// is the per-tunnel listener mode; Matcher and Registry are then
	// unused and may be nil.
	Dialer upstream.Dialer
	// Logger receives structured log lines. Required.
	Logger *log.Logger
}

// Server is a SOCKS5 server accepting no-auth CONNECT requests. Serve
// blocks until the context is cancelled or the listener closes.
type Server struct {
	opts Options
	mu   sync.Mutex
	ln   net.Listener
}

// NewServer returns a Server bound to opts.Listen. The listener is held
// until Serve runs; callers may inspect Addr() to discover the bound
// address (useful when Listen specifies port 0). The supplied context is
// used only for the listener handshake; Serve uses its own ctx.
func NewServer(ctx context.Context, opts Options) (*Server, error) {
	if opts.Logger == nil {
		return nil, errors.New("logger required")
	}

	if opts.Dialer == nil {
		if opts.Matcher == nil {
			return nil, errors.New("matcher required")
		}

		if opts.Registry == nil {
			return nil, errors.New("registry required")
		}
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", opts.Listen)
	if err != nil {
		return nil, errors.Wrap(err, "listen")
	}

	return &Server{opts: opts, ln: ln}, nil
}

// Addr returns the listener's resolved address (host:port).
func (s *Server) Addr() string {
	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()

	if ln == nil {
		return ""
	}

	return ln.Addr().String()
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

func (s *Server) pick(host string) upstream.Dialer {
	if s.opts.Dialer != nil {
		return s.opts.Dialer
	}

	if r, ok := s.opts.Matcher.First(host); ok {
		if d, ok := s.opts.Registry.Get(r.Upstream); ok {
			return d
		}
	}

	return s.opts.Registry.Direct()
}
