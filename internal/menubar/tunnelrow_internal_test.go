package menubar

import (
	"testing"

	"github.com/foomo/dockprox/pkg/sshclient"
)

var allConnStates = []sshclient.ConnState{
	sshclient.ConnUnknown,
	sshclient.ConnConnected,
	sshclient.ConnDisconnected,
	sshclient.ConnConnecting,
	sshclient.ConnAwaitingApproval,
}

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
			// A listener bound by Start() that nothing has dialled through
			// yet must not read as "stopped", because clicking it stops the
			// tunnel.
			name:      "listening, never dialled",
			status:    TunnelStatus{State: TunnelListening, Addr: "127.0.0.1:1080", ConnState: sshclient.ConnUnknown},
			wantGlyph: "◉",
			wantAddr:  "127.0.0.1:1080",
			wantAct:   tunnelActionStop,
		},
		{
			name:      "listening, dial failed",
			status:    TunnelStatus{State: TunnelListening, Addr: "127.0.0.1:1080", ConnState: sshclient.ConnDisconnected},
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

// TestTunnelRow_GlyphAgreesWithAction pins the invariant: ○ means "click to
// start" and every other glyph means "click to stop". A row that reads
// stopped while its click stops the tunnel makes the first click look like
// it did nothing.
func TestTunnelRow_GlyphAgreesWithAction(t *testing.T) {
	for _, tunnelState := range []TunnelState{TunnelListening, TunnelStopped} {
		for _, connState := range allConnStates {
			glyph, _, act := tunnelRow(TunnelStatus{State: tunnelState, ConnState: connState})

			if (glyph == "○") != (act == tunnelActionStart) {
				t.Errorf("State=%v ConnState=%v: glyph %q disagrees with action %v",
					tunnelState, connState, glyph, act)
			}
		}
	}
}

func TestConnRow(t *testing.T) {
	for _, tc := range []struct {
		conn sshclient.ConnState
		want string
	}{
		{sshclient.ConnUnknown, "◎ not connected"},
		{sshclient.ConnConnecting, "⊙ connecting"},
		{sshclient.ConnAwaitingApproval, "☎︎ awaiting approval (1Password menu → SSH request waiting)"},
		{sshclient.ConnDisconnected, "⚠︎ connection failed"},
		{sshclient.ConnConnected, "◉ connected"},
	} {
		got, ok := connRow(TunnelStatus{State: TunnelListening, ConnState: tc.conn})
		if !ok || got != tc.want {
			t.Errorf("ConnState=%v: connRow = %q, %v; want %q, true", tc.conn, got, ok, tc.want)
		}
	}
}

// TestConnRow_HiddenWhenStopped: a stopped tunnel has no connection to show,
// whatever state its client last reported.
func TestConnRow_HiddenWhenStopped(t *testing.T) {
	for _, connState := range allConnStates {
		if label, ok := connRow(TunnelStatus{State: TunnelStopped, ConnState: connState}); ok {
			t.Errorf("ConnState=%v: stopped tunnel shows conn row %q", connState, label)
		}
	}
}
