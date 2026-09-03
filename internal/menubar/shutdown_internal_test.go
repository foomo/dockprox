package menubar //nolint:testpackage // needs access to unexported waitDone

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWaitDone_AllClosed(t *testing.T) {
	a, b := make(chan struct{}), make(chan struct{})
	close(a)
	close(b)

	assert.True(t, waitDone([]chan struct{}{a, b}, time.Second))
}

func TestWaitDone_NilChannelsSkipped(t *testing.T) {
	// Stop passes the proxy's done channel straight through, and it is nil
	// when the proxy never started.
	assert.True(t, waitDone([]chan struct{}{nil, nil}, time.Second))
}

func TestWaitDone_TimesOutOnWedgedChannel(t *testing.T) {
	open := make(chan struct{}) // never closed: a wedged Serve goroutine

	start := time.Now()
	ok := waitDone([]chan struct{}{open}, 50*time.Millisecond)
	elapsed := time.Since(start)

	assert.False(t, ok)
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
	assert.Less(t, elapsed, time.Second, "must not block past the deadline")
}

// The deadline is shared across channels, so N wedged listeners cost one
// timeout rather than N.
func TestWaitDone_DeadlineIsSharedAcrossChannels(t *testing.T) {
	const timeout = 100 * time.Millisecond

	dones := make([]chan struct{}, 5)
	for i := range dones {
		dones[i] = make(chan struct{})
	}

	start := time.Now()
	ok := waitDone(dones, timeout)
	elapsed := time.Since(start)

	assert.False(t, ok)
	assert.Less(t, elapsed, 3*timeout,
		"deadline must be shared, not applied per channel")
}

func TestWaitDone_ReturnsWhenLaterChannelCloses(t *testing.T) {
	closed := make(chan struct{})
	close(closed)

	later := make(chan struct{})

	go func() {
		time.Sleep(20 * time.Millisecond)
		close(later)
	}()

	assert.True(t, waitDone([]chan struct{}{closed, later}, time.Second))
}
