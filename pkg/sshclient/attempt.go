package sshclient

import (
	"net"
	"sync"
	"time"
)

// attempt holds the sockets of one dial under a shared deadline, so the
// agent wait and the SSH handshake run on one clock and abort together.
type attempt struct {
	timeout  time.Duration
	approval time.Duration
	// onSign reports a pending agent sign request (true) and its outcome
	// (false). Optional.
	onSign func(pending bool)

	mu      sync.Mutex
	conns   []net.Conn
	at      time.Time
	aborted bool
}

func newAttempt(timeout, approval time.Duration) *attempt {
	return &attempt{timeout: timeout, approval: approval, at: time.Now().Add(timeout)}
}

func (a *attempt) add(c net.Conn) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.conns = append(a.conns, c)
	_ = c.SetDeadline(a.at)
}

func (a *attempt) extend(d time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.aborted {
		return
	}

	a.at = time.Now().Add(d)
	for _, c := range a.conns {
		_ = c.SetDeadline(a.at)
	}
}

func (a *attempt) abort() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.aborted = true
	a.at = time.Unix(1, 0)

	for _, c := range a.conns {
		_ = c.SetDeadline(a.at)
	}
}

func (a *attempt) signStart() {
	a.extend(a.approval)

	if a.onSign != nil {
		a.onSign(true)
	}
}

func (a *attempt) signDone() {
	a.extend(a.timeout)

	if a.onSign != nil {
		a.onSign(false)
	}
}
