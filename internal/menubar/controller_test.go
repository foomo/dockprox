package menubar_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/internal/menubar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestConfig(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("listen: 127.0.0.1:0\nlog_level: error\n"), 0o600))

	return path
}

func newTestLogger() *log.Logger {
	return log.NewWithOptions(os.Stderr, log.Options{Level: log.ErrorLevel})
}

func waitUntil(t *testing.T, want menubar.State, ctrl *menubar.ProxyController) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ctrl.Snapshot().State == want {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("controller did not reach state %v; got %v", want, ctrl.Snapshot().State)
}

func TestController_StartFromStoppedReachesRunning(t *testing.T) {
	cfg := writeTestConfig(t, t.TempDir())
	ctrl := menubar.New(cfg, newTestLogger())

	t.Cleanup(func() { _ = ctrl.Stop() })

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)

	snap := ctrl.Snapshot()
	assert.NotEmpty(t, snap.ListenAddr)
	assert.Equal(t, cfg, snap.ConfigPath)
}

func TestController_DoubleStartIsNoop(t *testing.T) {
	cfg := writeTestConfig(t, t.TempDir())
	ctrl := menubar.New(cfg, newTestLogger())

	t.Cleanup(func() { _ = ctrl.Stop() })

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)
	addr := ctrl.Snapshot().ListenAddr

	require.NoError(t, ctrl.Start())
	assert.Equal(t, addr, ctrl.Snapshot().ListenAddr)
}

func TestController_StopFromRunningReachesStopped(t *testing.T) {
	cfg := writeTestConfig(t, t.TempDir())
	ctrl := menubar.New(cfg, newTestLogger())

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)

	require.NoError(t, ctrl.Stop())
	waitUntil(t, menubar.StateStopped, ctrl)
}

func TestController_DoubleStopIsNoop(t *testing.T) {
	cfg := writeTestConfig(t, t.TempDir())
	ctrl := menubar.New(cfg, newTestLogger())

	require.NoError(t, ctrl.Stop())
	assert.Equal(t, menubar.StateStopped, ctrl.Snapshot().State)
}

func TestController_RestartCyclesState(t *testing.T) {
	cfg := writeTestConfig(t, t.TempDir())
	ctrl := menubar.New(cfg, newTestLogger())

	t.Cleanup(func() { _ = ctrl.Stop() })

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)

	require.NoError(t, ctrl.Restart())
	waitUntil(t, menubar.StateRunning, ctrl)
}

func TestController_InvalidConfigReachesError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte(": not valid yaml :::\n"), 0o600))

	ctrl := menubar.New(path, newTestLogger())
	err := ctrl.Start()
	require.Error(t, err)
	assert.Equal(t, menubar.StateError, ctrl.Snapshot().State)
	assert.Error(t, ctrl.Snapshot().LastError)
}

func TestController_SubscribeReceivesStateChanges(t *testing.T) {
	cfg := writeTestConfig(t, t.TempDir())
	ctrl := menubar.New(cfg, newTestLogger())

	t.Cleanup(func() { _ = ctrl.Stop() })

	var (
		mu     sync.Mutex
		states []menubar.State
	)

	unsub := ctrl.Subscribe(func(s menubar.Status) {
		mu.Lock()

		states = append(states, s.State)
		mu.Unlock()
	})
	t.Cleanup(unsub)

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)
	require.NoError(t, ctrl.Stop())
	waitUntil(t, menubar.StateStopped, ctrl)

	mu.Lock()
	defer mu.Unlock()

	assert.Contains(t, states, menubar.StateStarting)
	assert.Contains(t, states, menubar.StateRunning)
	assert.Contains(t, states, menubar.StateStopped)
}
