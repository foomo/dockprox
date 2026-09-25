package sshclient

// ConnState is the last known state of a Client's SSH connection, updated
// opportunistically by Get and Close. Every state but ConnConnecting is a
// passive signal: it reflects the outcome of the most recent call, not
// real-time connectivity. ConnConnecting is the exception — it is set while
// a dial is actually in flight.
type ConnState int

const (
	ConnUnknown      ConnState = iota // never attempted
	ConnConnected                     // last Get succeeded (fresh dial or live keepalive)
	ConnDisconnected                  // last Get attempt failed, or Close was called
	// ConnConnecting means a dial is in flight right now. Appended rather
	// than inserted so the existing values keep their numbers, and so
	// ConnUnknown stays the zero value.
	ConnConnecting
	// ConnAwaitingApproval means a dial is in flight and the agent is
	// holding a sign request, waiting for the user to approve it.
	ConnAwaitingApproval
)

// String returns the lowercase state name.
func (s ConnState) String() string {
	switch s {
	case ConnConnected:
		return "connected"
	case ConnDisconnected:
		return "disconnected"
	case ConnConnecting:
		return "connecting"
	case ConnAwaitingApproval:
		return "awaiting approval"
	default:
		return "unknown"
	}
}
