package cli

import (
	"encoding/binary"
	"io"
	"net"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/match"
	"github.com/foomo/dockprox/pkg/upstream"
)

// socksReply opens a SOCKS5 CONNECT to host:443 through addr and returns
// the reply code.
func socksReply(t *testing.T, addr, host string) byte {
	t.Helper()

	c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	req := []byte{0x05, 0x01, 0x00, 0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	req = binary.BigEndian.AppendUint16(req, 443)

	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}

	resp := make([]byte, 2+10) // method selection + reply
	if _, err := io.ReadFull(c, resp); err != nil {
		t.Fatal(err)
	}

	return resp[3]
}

func TestTunnelListeners_PinnedAndRuleRouted(t *testing.T) {
	backend, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()

	go func() {
		for {
			c, err := backend.Accept()
			if err != nil {
				return
			}

			_ = c.Close()
		}
	}()

	cfg := &config.Config{
		Listen:    "127.0.0.1:0",
		Upstreams: map[string]config.Upstream{"fwd": {Type: config.UpstreamForward, Addr: backend.Addr().String()}},
		Rules:     []config.Rule{{Match: "*.routed.test", Upstream: "fwd"}},
		Listeners: []config.Listener{
			{Listen: "127.0.0.1:0", Upstream: "fwd"},
			{Listen: "127.0.0.1:0"}, // two ":0" fail Validate, which this test skips
		},
	}

	reg, err := upstream.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m, err := match.New([]match.Rule{{Pattern: "*.routed.test", Upstream: "fwd"}}, map[string]struct{}{"fwd": {}})
	if err != nil {
		t.Fatal(err)
	}

	srvs, err := tunnelListeners(t.Context(), cfg, reg, m, log.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range srvs {
		go func() { _ = s.Serve(t.Context()) }()
	}

	pinned, routed := srvs[0].Addr(), srvs[1].Addr()

	if got := socksReply(t, pinned, "unmatched.invalid"); got != 0x00 {
		t.Fatalf("pinned listener: reply=%d, want 0 (rules ignored)", got)
	}

	if got := socksReply(t, routed, "a.routed.test"); got != 0x00 {
		t.Fatalf("routed listener, matched host: reply=%d, want 0", got)
	}

	if got := socksReply(t, routed, "unmatched.invalid"); got == 0x00 {
		t.Fatal("routed listener, unmatched host: succeeded, want direct dial to fail")
	}
}
