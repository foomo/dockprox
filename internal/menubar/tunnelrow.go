package menubar

import (
	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/sshclient"
)

// tunnelAction is what clicking a tunnel's menu row does.
type tunnelAction int

const (
	// tunnelActionStart binds the tunnel's listener (and connects it).
	tunnelActionStart tunnelAction = iota
	// tunnelActionStop releases the tunnel's listener.
	tunnelActionStop
)

// tunnelRow decides how one listener's row is presented: the glyph, the
// label, and what a click does. Labels:
//
//	<listen> → <upstream>                  socks5, pinned
//	<listen>                               socks5, routed by rules
//	<listen> → <target> via <upstream>     tcp, pinned
//	<listen> → <target>                    tcp, routed by rules
//
//	○  stopped — no listener   (click starts it)
//	◉  listening               (click stops it)
//
// Glyph and action are derived together, in one place, because they must
// agree — a row that reads "stopped" must start the tunnel, and a row that
// reads "up" must stop it. The connection of a pinned upstream is shown on
// that upstream's own row, see upstreamRow.
func tunnelRow(ts TunnelStatus) (glyph, label string, action tunnelAction) { //nolint:nonamedreturns // named for the doc comment's sake
	label = ts.Name

	switch {
	case ts.Target != "" && ts.Upstream != "":
		label += " → " + ts.Target + " via " + ts.Upstream
	case ts.Target != "":
		label += " → " + ts.Target
	case ts.Upstream != "":
		label += " → " + ts.Upstream
	}

	if ts.State != TunnelListening {
		return "○", label, tunnelActionStart
	}

	return "◉", label, tunnelActionStop
}

// upstreamRow decides how one ssh or openconnect upstream's row is
// presented ("<name> (<type>): <state>") and what a click does: connect
// when it is down, disconnect when it is up or connecting (which aborts the
// attempt, e.g. an MFA push nobody will approve).
//
//	○  disconnected            (click connects)
//	⊙  connecting              (click aborts)
//	☎︎  waiting on an approval  (click aborts)
//	⚠︎  last attempt failed or the connection dropped  (click connects)
//	◉  connected               (click disconnects)
func upstreamRow(us UpstreamStatus) (glyph, label string, connect bool) { //nolint:nonamedreturns // named for the doc comment's sake
	prefix := us.Name + " (" + us.Type + "): "

	switch us.ConnState {
	case sshclient.ConnConnected:
		return "◉", prefix + "connected", false
	case sshclient.ConnConnecting:
		if us.Type == config.UpstreamOpenConnect {
			// The VPN login waits on the user's MFA device.
			return "⊙", prefix + "connecting (approve on your device)", false
		}

		return "⊙", prefix + "connecting", false
	case sshclient.ConnAwaitingApproval:
		return "☎︎", prefix + "awaiting approval", false
	case sshclient.ConnDisconnected:
		return "⚠︎", prefix + "failed", true
	case sshclient.ConnUnknown:
	}

	return "○", prefix + "disconnected", true
}
