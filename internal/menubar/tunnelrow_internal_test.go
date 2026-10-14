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
		wantLabel string
		wantAct   tunnelAction
	}{
		{
			name:      "pinned, stopped",
			status:    TunnelStatus{Name: "127.0.0.1:1080", Upstream: "bastion", State: TunnelStopped},
			wantGlyph: "○",
			wantLabel: "127.0.0.1:1080 → bastion",
			wantAct:   tunnelActionStart,
		},
		{
			name:      "pinned, listening",
			status:    TunnelStatus{Name: "127.0.0.1:1080", Upstream: "bastion", State: TunnelListening, Addr: "127.0.0.1:1080"},
			wantGlyph: "◉",
			wantLabel: "127.0.0.1:1080 → bastion",
			wantAct:   tunnelActionStop,
		},
		{
			name:      "tcp, pinned",
			status:    TunnelStatus{Name: "127.0.0.1:5432", Upstream: "bastion", Target: "db.internal:5432", State: TunnelListening},
			wantGlyph: "◉",
			wantLabel: "127.0.0.1:5432 → db.internal:5432 via bastion",
			wantAct:   tunnelActionStop,
		},
		{
			name:      "tcp, routed by rules",
			status:    TunnelStatus{Name: "127.0.0.1:5432", Target: "db.internal:5432", State: TunnelStopped},
			wantGlyph: "○",
			wantLabel: "127.0.0.1:5432 → db.internal:5432",
			wantAct:   tunnelActionStart,
		},
		{
			name:      "routed by rules",
			status:    TunnelStatus{Name: "127.0.0.1:1090", State: TunnelListening, Addr: "127.0.0.1:1090"},
			wantGlyph: "◉",
			wantLabel: "127.0.0.1:1090",
			wantAct:   tunnelActionStop,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			glyph, label, act := tunnelRow(tc.status)
			if glyph != tc.wantGlyph || label != tc.wantLabel || act != tc.wantAct {
				t.Fatalf("tunnelRow = %q, %q, %v; want %q, %q, %v",
					glyph, label, act, tc.wantGlyph, tc.wantLabel, tc.wantAct)
			}
		})
	}
}

func TestUpstreamRow(t *testing.T) {
	for _, tc := range []struct {
		status      UpstreamStatus
		wantGlyph   string
		wantLabel   string
		wantConnect bool
	}{
		{UpstreamStatus{Name: "bastion", Type: "ssh"}, "○", "bastion (ssh): disconnected", true},
		{UpstreamStatus{Name: "bastion", Type: "ssh", ConnState: sshclient.ConnConnecting}, "⊙", "bastion (ssh): connecting", false},
		{UpstreamStatus{Name: "bastion", Type: "ssh", ConnState: sshclient.ConnAwaitingApproval}, "☎︎", "bastion (ssh): awaiting approval", false},
		{UpstreamStatus{Name: "bastion", Type: "ssh", ConnState: sshclient.ConnDisconnected}, "⚠︎", "bastion (ssh): failed", true},
		{UpstreamStatus{Name: "vpn", Type: "openconnect", ConnState: sshclient.ConnConnected}, "◉", "vpn (openconnect): connected", false},
		{UpstreamStatus{Name: "vpn", Type: "openconnect", ConnState: sshclient.ConnConnecting}, "⊙", "vpn (openconnect): connecting (approve on your device)", false},
	} {
		t.Run(tc.wantLabel, func(t *testing.T) {
			glyph, label, connect := upstreamRow(tc.status)
			if glyph != tc.wantGlyph || label != tc.wantLabel || connect != tc.wantConnect {
				t.Fatalf("upstreamRow = %q, %q, %v; want %q, %q, %v",
					glyph, label, connect, tc.wantGlyph, tc.wantLabel, tc.wantConnect)
			}
		})
	}
}

// TestUpstreamRow_GlyphAgreesWithAction pins that only a row reading down
// (○, ⚠︎) connects on click, for every state.
func TestUpstreamRow_GlyphAgreesWithAction(t *testing.T) {
	for _, s := range allConnStates {
		glyph, _, connect := upstreamRow(UpstreamStatus{Name: "x", Type: "ssh", ConnState: s})

		down := glyph == "○" || glyph == "⚠︎"
		if down != connect {
			t.Fatalf("state %s: glyph %q but connect=%v", s, glyph, connect)
		}
	}
}
