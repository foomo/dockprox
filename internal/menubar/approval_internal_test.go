package menubar

import (
	"testing"

	"github.com/foomo/dockprox/pkg/sshclient"
	"github.com/stretchr/testify/assert"
)

// TestSSHStateChanged_ApprovalEdges pins that onApproval sees each entry to
// and exit from ConnAwaitingApproval exactly once, however many other state
// changes come in between.
func TestSSHStateChanged_ApprovalEdges(t *testing.T) {
	c := New("", nil)

	var got []bool

	c.OnApproval(func(pending bool) { got = append(got, pending) })

	state := sshclient.ConnUnknown
	hook := c.sshStateChanged(func() sshclient.ConnState { return state })

	for _, s := range []sshclient.ConnState{
		sshclient.ConnConnecting,
		sshclient.ConnAwaitingApproval,
		sshclient.ConnAwaitingApproval,
		sshclient.ConnConnecting,
		sshclient.ConnConnected,
		sshclient.ConnAwaitingApproval,
		sshclient.ConnDisconnected,
	} {
		state = s

		hook()
	}

	assert.Equal(t, []bool{true, false, true, false}, got)
}
