package menubar_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/foomo/dockprox/internal/menubar"
	"github.com/foomo/dockprox/pkg/sshclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func writeTestConfig(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("listen: 127.0.0.1:0\nlog_level: error\n"), 0o600))

	return path
}

// writeTestKey generates an unencrypted ed25519 private key in dir and
// returns its path. No SSH dial is ever attempted against it in these
// tests — only the tunnel's SOCKS5 listener bind is exercised, per the
// lazy-connect design (Client.Get is never called by Start/Stop/Restart).
func writeTestKey(t *testing.T, dir string) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	block, err := ssh.MarshalPrivateKey(priv, "")
	require.NoError(t, err)

	path := filepath.Join(dir, "id_ed25519")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))

	return path
}

// fixedFingerprint returns a syntactically valid SHA256:<base64> host key
// fingerprint that config/sshclient validation accepts. It never needs to
// match anything real since these tests never dial.
func fixedFingerprint() string {
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
}

// writeTunnelTestConfig writes a config with one type: ssh upstream named
// "jump" bound to socks5Listen, plus a rule so it isn't rejected as
// unreferenced dead config.
func writeTunnelTestConfig(t *testing.T, dir, socks5Listen string) string {
	t.Helper()

	key := writeTestKey(t, dir)

	yaml := fmt.Sprintf(`listen: 127.0.0.1:0
log_level: error
upstreams:
  jump:
    type: ssh
    host: bastion.invalid
    keyFile: %s
    hostKey: %q
    socks5Listen: %s
rules:
  - match: "*.internal.invalid"
    upstream: jump
`, key, fixedFingerprint(), socks5Listen)

	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	return path
}

// writeTwoTunnelTestConfig writes a config with two type: ssh upstreams,
// each with its own fixed socks5Listen address (not :0, so the test can
// dial the untouched tunnel's address after stopping the other).
func writeTwoTunnelTestConfig(t *testing.T, dir, firstListen, secondListen string) string {
	t.Helper()

	key := writeTestKey(t, dir)
	fp := fixedFingerprint()

	yaml := fmt.Sprintf(`listen: 127.0.0.1:0
log_level: error
upstreams:
  first:
    type: ssh
    host: bastion1.invalid
    keyFile: %[1]s
    hostKey: %[2]q
    socks5Listen: %[3]s
  second:
    type: ssh
    host: bastion2.invalid
    keyFile: %[1]s
    hostKey: %[2]q
    socks5Listen: %[4]s
rules:
  - match: "*.internal.invalid"
    upstream: first
  - match: "*.other.invalid"
    upstream: second
`, key, fp, firstListen, secondListen)

	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	return path
}

// freePort returns a currently-unused loopback TCP address suitable for a
// fixed (non-:0) test listener.
func freePort(t *testing.T) string {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	return addr
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

func TestController_StartWithTunnelReachesListening(t *testing.T) {
	dir := t.TempDir()
	cfg := writeTunnelTestConfig(t, dir, "127.0.0.1:0")
	ctrl := menubar.New(cfg, newTestLogger())

	t.Cleanup(func() { _ = ctrl.Stop() })

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)

	tunnels := ctrl.Snapshot().Tunnels
	require.Len(t, tunnels, 1)
	assert.Equal(t, "jump", tunnels[0].Name)
	assert.Equal(t, menubar.TunnelListening, tunnels[0].State)
	assert.NotEmpty(t, tunnels[0].Addr)
	assert.Equal(t, sshclient.ConnUnknown, tunnels[0].ConnState, "no dial has happened yet")
}

func TestController_StopTunnelDoesNotAffectProxyOrOtherTunnels(t *testing.T) {
	dir := t.TempDir()
	firstAddr := freePort(t)
	secondAddr := freePort(t)
	cfg := writeTwoTunnelTestConfig(t, dir, firstAddr, secondAddr)
	ctrl := menubar.New(cfg, newTestLogger())

	t.Cleanup(func() { _ = ctrl.Stop() })

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)

	require.NoError(t, ctrl.StopTunnel("first"))

	snap := ctrl.Snapshot()
	assert.Equal(t, menubar.StateRunning, snap.State)
	assert.NotEmpty(t, snap.ListenAddr)

	byName := map[string]menubar.TunnelStatus{}
	for _, ts := range snap.Tunnels {
		byName[ts.Name] = ts
	}

	assert.Equal(t, menubar.TunnelStopped, byName["first"].State)
	assert.Empty(t, byName["first"].Addr)

	assert.Equal(t, menubar.TunnelListening, byName["second"].State)
	assert.Equal(t, secondAddr, byName["second"].Addr)

	c, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", secondAddr)
	require.NoError(t, err, "second tunnel's listener should still accept connections")

	_ = c.Close()

	_, err = (&net.Dialer{}).DialContext(context.Background(), "tcp", firstAddr)
	assert.Error(t, err, "first tunnel's listener should have released its port")
}

func TestController_StartTunnelRebindsStoppedTunnel(t *testing.T) {
	dir := t.TempDir()
	firstAddr := freePort(t)
	secondAddr := freePort(t)
	cfg := writeTwoTunnelTestConfig(t, dir, firstAddr, secondAddr)
	ctrl := menubar.New(cfg, newTestLogger())

	t.Cleanup(func() { _ = ctrl.Stop() })

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)
	require.NoError(t, ctrl.StopTunnel("first"))

	require.NoError(t, ctrl.StartTunnel("first"))

	snap := ctrl.Snapshot()
	for _, ts := range snap.Tunnels {
		if ts.Name == "first" {
			assert.Equal(t, menubar.TunnelListening, ts.State)
			assert.Equal(t, firstAddr, ts.Addr)
		}
	}

	c, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", firstAddr)
	require.NoError(t, err)

	_ = c.Close()
}

func TestController_StopTunnelUnknownNameErrors(t *testing.T) {
	dir := t.TempDir()
	cfg := writeTunnelTestConfig(t, dir, "127.0.0.1:0")
	ctrl := menubar.New(cfg, newTestLogger())

	t.Cleanup(func() { _ = ctrl.Stop() })

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)

	err := ctrl.StopTunnel("does-not-exist")
	require.Error(t, err)
	assert.Equal(t, menubar.StateRunning, ctrl.Snapshot().State)
}

func TestController_StartTunnelWhenControllerStoppedErrors(t *testing.T) {
	cfg := writeTestConfig(t, t.TempDir())
	ctrl := menubar.New(cfg, newTestLogger())

	err := ctrl.StartTunnel("anything")
	assert.Error(t, err)
}

func TestController_StopWhileTunnelStoppedTearsDownCleanly(t *testing.T) {
	dir := t.TempDir()
	cfg := writeTunnelTestConfig(t, dir, "127.0.0.1:0")
	ctrl := menubar.New(cfg, newTestLogger())

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)
	require.NoError(t, ctrl.StopTunnel("jump"))

	done := make(chan error, 1)
	go func() { done <- ctrl.Stop() }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2s after StopTunnel")
	}

	waitUntil(t, menubar.StateStopped, ctrl)
}
