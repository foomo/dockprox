package menubar_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/foomo/dockprox/internal/menubar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listenAddr starts a TCP listener that is closed on cleanup and returns
// its address.
func listenAddr(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	t.Cleanup(func() { _ = l.Close() })

	return l.Addr().String()
}

func TestProbeForwards_ReachableAndUnreachable(t *testing.T) {
	t.Parallel()

	up := listenAddr(t)
	// A port that was bound and released: nothing is listening, so the
	// dial gets a prompt connection refused rather than a timeout.
	down := freePort(t)

	got := menubar.ProbeForwards(context.Background(), []menubar.ForwardStatus{
		{Name: "up", Addr: up},
		{Name: "down", Addr: down},
	})

	if len(got) != 2 {
		t.Fatalf("verdicts = %d, want 2", len(got))
	}

	if !got["up"] {
		t.Errorf("up = false, want true (listener on %s)", up)
	}

	if got["down"] {
		t.Errorf("down = true, want false (nothing listening on %s)", down)
	}
}

func TestProbeForwards_Empty(t *testing.T) {
	t.Parallel()

	if got := menubar.ProbeForwards(context.Background(), nil); len(got) != 0 {
		t.Errorf("verdicts = %v, want empty", got)
	}
}

func TestProbeForwards_CancelledContextReportsDown(t *testing.T) {
	t.Parallel()

	addr := listenAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// The endpoint is reachable, but the caller's context is already
	// done: the probe must report down rather than block or panic.
	if got := menubar.ProbeForwards(ctx, []menubar.ForwardStatus{
		{Name: "up", Addr: addr},
	}); got["up"] {
		t.Error("up = true, want false with a cancelled context")
	}
}

// writeForwardTestConfig writes a config with two type: forward upstreams
// (deliberately out of alphabetical order) plus a non-forward upstream, so
// the test covers both filtering and sorting. Rules reference every
// upstream, since unreferenced ones are rejected as dead config.
func writeForwardTestConfig(t *testing.T, dir, zAddr, aAddr string) string {
	t.Helper()

	yaml := fmt.Sprintf(`listen: 127.0.0.1:0
log_level: error
upstreams:
  zeta:
    type: forward
    addr: %s
  alpha:
    type: forward
    addr: %s
  plain:
    type: direct
rules:
  - match: zeta.example.com
    upstream: zeta
  - match: alpha.example.com
    upstream: alpha
  - match: plain.example.com
    upstream: plain
`, zAddr, aAddr)

	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	return path
}

func TestProxyController_SnapshotForwards(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeForwardTestConfig(t, dir, "127.0.0.1:9001", "127.0.0.1:9002")

	ctrl := menubar.New(path, newTestLogger())

	// Stopped: no config is loaded, so there is nothing to report.
	require.Empty(t, ctrl.Snapshot().Forwards)

	require.NoError(t, ctrl.Start())
	t.Cleanup(func() { _ = ctrl.Stop() })

	// Only the forwards, sorted by name — the direct upstream is excluded.
	assert.Equal(t, []menubar.ForwardStatus{
		{Name: "alpha", Addr: "127.0.0.1:9002"},
		{Name: "zeta", Addr: "127.0.0.1:9001"},
	}, ctrl.Snapshot().Forwards)

	require.NoError(t, ctrl.Stop())
	assert.Empty(t, ctrl.Snapshot().Forwards)
}
