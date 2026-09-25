package menubar

import "github.com/foomo/dockprox/pkg/sshclient"

// tunnelAction is what clicking a tunnel's menu row does.
type tunnelAction int

const (
	// tunnelActionStart binds the tunnel's listener (and connects it).
	tunnelActionStart tunnelAction = iota
	// tunnelActionStop releases the tunnel's listener.
	tunnelActionStop
)

// tunnelRow decides how one tunnel's listener row is presented: the glyph,
// the address column, and what a click does.
//
//	○  stopped — no listener   (click starts it)
//	◉  listening               (click stops it)
//
// Glyph and action are derived together, in one place, because they must
// agree — a row that reads "stopped" must start the tunnel, and a row that
// reads "up" must stop it. The SSH connection behind the listener is shown
// on its own row, see connRow.
func tunnelRow(ts TunnelStatus) (glyph, addr string, action tunnelAction) { //nolint:nonamedreturns // named for the doc comment's sake
	if ts.State != TunnelListening {
		return "○", "-", tunnelActionStart
	}

	return "◉", ts.Addr, tunnelActionStop
}

// connRow returns the label of the informational row shown under a
// listening tunnel, describing its SSH connection. ok is false when the
// tunnel is stopped and the row is omitted.
//
//	◎  never dialled — the listener dials lazily
//	⊙  dial in flight
//	☎︎  the agent is waiting for the user to approve a sign request
//	⚠︎  last dial failed
//	◉  connected
func connRow(ts TunnelStatus) (label string, ok bool) { //nolint:nonamedreturns // named for the doc comment's sake
	if ts.State != TunnelListening {
		return "", false
	}

	switch ts.ConnState {
	case sshclient.ConnConnected:
		return "◉ ssh connected", true
	case sshclient.ConnConnecting:
		return "⊙ ssh connecting", true
	case sshclient.ConnAwaitingApproval:
		return "☎︎ ssh awaiting approval", true
	case sshclient.ConnDisconnected:
		// Client.Close also sets this, but a closed client belongs to a
		// tunnel that is no longer listening, so here it always means a
		// failed attempt.
		return "⚠︎ ssh connection failed", true
	case sshclient.ConnUnknown:
	}

	return "◎ ssh not connected", true
}
