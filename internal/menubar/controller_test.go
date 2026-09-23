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
// returns its path. Start/Stop/Restart never dial with it, per the
// lazy-connect design; StartTunnel does, since it connects eagerly, so the
// configs that back a StartTunnel test point at either an unresolvable host
// (to exercise the failure path) or a real in-process server.
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
// match anything real: the configs using it point at unresolvable hosts, so
// the dial fails before any host key is presented.
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

// waitTunnelConn polls until the named tunnel reports want, or fails. The
// eager connect StartTunnel kicks off runs in the background, so its result
// lands after StartTunnel has already returned.
func waitTunnelConn(
	t *testing.T,
	ctrl *menubar.ProxyController,
	name string,
	want sshclient.ConnState,
) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	var got sshclient.ConnState

	for time.Now().Before(deadline) {
		for _, ts := range ctrl.Snapshot().Tunnels {
			if ts.Name == name {
				if got = ts.ConnState; got == want {
					return
				}
			}
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("tunnel %q ConnState = %v; want %v", name, got, want)
}

// startTestSSHServer runs an in-process SSH server that accepts any auth and
// rejects channel opens, which is enough for the handshake that Connect
// performs. Returns its address and host key fingerprint.
func startTestSSHServer(t *testing.T) (addr, hostFP string) { //nolint:nonamedreturns
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)

	cfg := &ssh.ServerConfig{
		NoClientAuth: true,
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(raw, cfg)
				if err != nil {
					_ = raw.Close()

					return
				}

				defer sc.Close()

				go func() {
					for nc := range chans {
						_ = nc.Reject(ssh.Prohibited, "no channels")
					}
				}()

				for r := range reqs {
					if r.WantReply {
						_ = r.Reply(true, nil)
					}
				}
			}()
		}
	}()

	return ln.Addr().String(), ssh.FingerprintSHA256(signer.PublicKey())
}

// writeReachableTunnelConfig writes a config whose "jump" ssh upstream points
// at a real, reachable in-process server, so the eager connect succeeds.
func writeReachableTunnelConfig(t *testing.T, dir, sshAddr, hostFP, socks5Listen string) string {
	t.Helper()

	host, portStr, err := net.SplitHostPort(sshAddr)
	require.NoError(t, err)

	yaml := fmt.Sprintf(`listen: 127.0.0.1:0
log_level: error
upstreams:
  jump:
    type: ssh
    host: %s
    port: %s
    user: tester
    keyFile: %s
    hostKey: %q
    socks5Listen: %s
rules:
  - match: "*.internal.invalid"
    upstream: jump
`, host, portStr, writeTestKey(t, dir), hostFP, socks5Listen)

	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))

	return path
}

func TestController_StartTunnelConnectsSSHEagerly(t *testing.T) {
	dir := t.TempDir()
	sshAddr, hostFP := startTestSSHServer(t)
	listen := freePort(t)
	cfg := writeReachableTunnelConfig(t, dir, sshAddr, hostFP, listen)
	ctrl := menubar.New(cfg, newTestLogger())

	t.Cleanup(func() { _ = ctrl.Stop() })

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)

	// Start alone stays lazy: binding the listener must not dial.
	require.NoError(t, ctrl.StopTunnel("jump"))

	// Clicking the tunnel row brings up both the listener and the SSH
	// connection, so the menu can show it connected without any traffic
	// having passed through it.
	require.NoError(t, ctrl.StartTunnel("jump"))

	waitTunnelConn(t, ctrl, "jump", sshclient.ConnConnected)

	for _, ts := range ctrl.Snapshot().Tunnels {
		if ts.Name == "jump" {
			assert.Equal(t, menubar.TunnelListening, ts.State)
		}
	}
}

// startStalledSSHServer accepts TCP but never speaks SSH, so a dial against
// it stays parked in the handshake for the life of the test.
func startStalledSSHServer(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}

			go func() {
				defer conn.Close()

				<-t.Context().Done()
			}()
		}
	}()

	return ln.Addr().String()
}

func TestController_StartTunnelReportsConnectingWhileDialing(t *testing.T) {
	dir := t.TempDir()
	listen := freePort(t)
	cfg := writeReachableTunnelConfig(t, dir, startStalledSSHServer(t), fixedFingerprint(), listen)
	ctrl := menubar.New(cfg, newTestLogger())

	t.Cleanup(func() { _ = ctrl.Stop() })

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)
	require.NoError(t, ctrl.StopTunnel("jump"))

	require.NoError(t, ctrl.StartTunnel("jump"))

	// The dial parks in the handshake, so the menu has a real window in
	// which to show the connecting glyph rather than a stale idle one.
	waitTunnelConn(t, ctrl, "jump", sshclient.ConnConnecting)
}

func TestController_StartTunnelKeepsListeningWhenSSHUnreachable(t *testing.T) {
	dir := t.TempDir()
	listen := freePort(t)
	// bastion.invalid never resolves, so the eager connect fails.
	cfg := writeTunnelTestConfig(t, dir, listen)
	ctrl := menubar.New(cfg, newTestLogger())

	t.Cleanup(func() { _ = ctrl.Stop() })

	require.NoError(t, ctrl.Start())
	waitUntil(t, menubar.StateRunning, ctrl)
	require.NoError(t, ctrl.StopTunnel("jump"))

	// A failing dial must not fail the click: the listener is what the
	// caller asked for, and connections still retry lazily.
	require.NoError(t, ctrl.StartTunnel("jump"))

	waitTunnelConn(t, ctrl, "jump", sshclient.ConnDisconnected)

	snap := ctrl.Snapshot()
	assert.Equal(t, menubar.StateRunning, snap.State, "a failed ssh dial must not fault the proxy")

	for _, ts := range snap.Tunnels {
		if ts.Name == "jump" {
			assert.Equal(t, menubar.TunnelListening, ts.State, "listener stays up despite the failed dial")
		}
	}

	c, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", listen)
	require.NoError(t, err, "listener should still accept connections")

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
