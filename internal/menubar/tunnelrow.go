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

// tunnelRow decides how one tunnel is presented: the glyph, the address
// column, and what a click does.
//
//	○  stopped — no listener              (click starts it)
//	◎  listening, never dialled           (click stops it)
//	⊙  listening, dial in flight          (click stops it)
//	⚠︎  listening, last dial failed        (click stops it)
//	◉  listening and connected            (click stops it)
//
// Glyph and action are derived together, in one place, because they must
// agree — a row that reads "stopped" must start the tunnel, and a row that
// reads "up" must stop it. Deriving them separately is what previously let
// a listening-but-unconnected tunnel render as ○ ("stopped") while its
// click still called StopTunnel, so the first click appeared to do nothing
// and the tunnel only came up on the second.
func tunnelRow(ts TunnelStatus) (glyph, addr string, action tunnelAction) { //nolint:nonamedreturns // named for the doc comment's sake
	if ts.State != TunnelListening {
		return "○", "-", tunnelActionStart
	}

	// The listener is bound, so the row is "up" and a click stops it. The
	// glyph then distinguishes how far the SSH connection behind it has
	// got, which is what the user is actually waiting on.
	glyph = "◎"

	switch ts.ConnState {
	case sshclient.ConnConnected:
		glyph = "◉"
	case sshclient.ConnConnecting:
		glyph = "⊙"
	case sshclient.ConnDisconnected:
		// The last dial failed. Client.Close also sets this, but a closed
		// client belongs to a tunnel that is no longer listening, so on a
		// listening tunnel this always means a failed attempt — worth
		// flagging, since the usual causes (a locked password manager, an
		// unreachable bastion) need the user to do something.
		glyph = "⚠︎"
	case sshclient.ConnUnknown:
		// Bound but never dialled: nothing has gone wrong, and a
		// connection through the listener will dial lazily.
	}

	return glyph, ts.Addr, tunnelActionStop
}
