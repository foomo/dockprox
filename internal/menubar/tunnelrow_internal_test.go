package menubar

import (
	"testing"

	"github.com/foomo/dockprox/pkg/sshclient"
)

func TestTunnelRow(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    TunnelStatus
		wantGlyph string
		wantAddr  string
		wantAct   tunnelAction
	}{
		{
			name:      "stopped",
			status:    TunnelStatus{State: TunnelStopped, ConnState: sshclient.ConnUnknown},
			wantGlyph: "○",
			wantAddr:  "-",
			wantAct:   tunnelActionStart,
		},
		{
			// The regression: a listener bound by Start() that nothing has
			// dialled through yet. It must not read as "stopped", because
			// clicking it stops the tunnel.
			name:      "listening, never dialled",
			status:    TunnelStatus{State: TunnelListening, Addr: "127.0.0.1:1080", ConnState: sshclient.ConnUnknown},
			wantGlyph: "◎",
			wantAddr:  "127.0.0.1:1080",
			wantAct:   tunnelActionStop,
		},
		{
			name:      "listening, dial in flight",
			status:    TunnelStatus{State: TunnelListening, Addr: "127.0.0.1:1080", ConnState: sshclient.ConnConnecting},
			wantGlyph: "⊙",
			wantAddr:  "127.0.0.1:1080",
			wantAct:   tunnelActionStop,
		},
		{
			// A locked password manager or an unreachable bastion lands
			// here. It must be distinguishable from the never-dialled row
			// above, which looks identical otherwise.
			name:      "listening, dial failed",
			status:    TunnelStatus{State: TunnelListening, Addr: "127.0.0.1:1080", ConnState: sshclient.ConnDisconnected},
			wantGlyph: "⚠︎",
			wantAddr:  "127.0.0.1:1080",
			wantAct:   tunnelActionStop,
		},
		{
			name:      "listening and connected",
			status:    TunnelStatus{State: TunnelListening, Addr: "127.0.0.1:1080", ConnState: sshclient.ConnConnected},
			wantGlyph: "◉",
			wantAddr:  "127.0.0.1:1080",
			wantAct:   tunnelActionStop,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			glyph, addr, act := tunnelRow(tc.status)

			if glyph != tc.wantGlyph {
				t.Errorf("glyph = %q, want %q", glyph, tc.wantGlyph)
			}

			if addr != tc.wantAddr {
				t.Errorf("addr = %q, want %q", addr, tc.wantAddr)
			}

			if act != tc.wantAct {
				t.Errorf("action = %v, want %v", act, tc.wantAct)
			}
		})
	}
}

// TestTunnelRow_FailedIsDistinctFromIdle pins the distinction that made a
// locked-1Password tunnel unreadable: a failed dial and a tunnel nothing has
// dialled through are both "listening, not connected", but only one of them
// is a problem the user has to act on.
func TestTunnelRow_FailedIsDistinctFromIdle(t *testing.T) {
	idle, _, _ := tunnelRow(TunnelStatus{State: TunnelListening, ConnState: sshclient.ConnUnknown})
	failed, _, _ := tunnelRow(TunnelStatus{State: TunnelListening, ConnState: sshclient.ConnDisconnected})

	if idle == failed {
		t.Errorf("idle and failed tunnels share glyph %q", idle)
	}
}

// TestTunnelRow_GlyphAgreesWithAction pins the invariant that the previous
// bug broke: ○ means "click to start" and every other glyph means "click to
// stop". A row that reads stopped while its click stops the tunnel makes
// the first click look like it did nothing.
func TestTunnelRow_GlyphAgreesWithAction(t *testing.T) {
	states := []sshclient.ConnState{
		sshclient.ConnUnknown,
		sshclient.ConnConnected,
		sshclient.ConnDisconnected,
		sshclient.ConnConnecting,
	}

	for _, tunnelState := range []TunnelState{TunnelListening, TunnelStopped} {
		for _, connState := range states {
			glyph, _, act := tunnelRow(TunnelStatus{State: tunnelState, ConnState: connState})

			if (glyph == "○") != (act == tunnelActionStart) {
				t.Errorf("State=%v ConnState=%v: glyph %q disagrees with action %v",
					tunnelState, connState, glyph, act)
			}
		}
	}
}
