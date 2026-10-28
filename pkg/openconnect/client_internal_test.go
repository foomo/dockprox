package openconnect

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/sshclient"
)

// fakeEnv switches the test binary into a fake openconnect: it checks the
// password on stdin, then listens where ocproxy would. Values: "ok", "hang"
// (never comes up), "tun" (prints the tunnel-up line and blocks),
// "stubborn" (like tun, but ignores SIGTERM).
const fakeEnv = "DOCKPROX_FAKE_OPENCONNECT"

// Env the fake sudo script reads: where to log its arguments, and a
// non-zero exit for `sudo -n -l` to fake a missing sudoers line.
const (
	fakeSudoLogEnv  = "DOCKPROX_FAKE_SUDO_LOG"
	fakeSudoDenyEnv = "DOCKPROX_FAKE_SUDO_DENY"
	// fakeSudoNoExecEnv makes the fake sudo log its arguments and exit
	// without running them, for scripts that would touch /etc.
	fakeSudoNoExecEnv = "DOCKPROX_FAKE_SUDO_NOEXEC"
)

// fakeSudoScript logs its arguments, answers -l from the env, and otherwise
// runs the command in place so its pid is the one dockprox signals.
const fakeSudoScript = `#!/bin/sh
printf '%s\n' "$*" >> "$` + fakeSudoLogEnv + `"
if [ "$2" = "-l" ]; then exit "${` + fakeSudoDenyEnv + `:-0}"; fi
if [ -n "$` + fakeSudoNoExecEnv + `" ]; then exit 0; fi
shift
exec "$@"
`

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		fakeOpenconnect(mode)
		return
	}

	os.Exit(m.Run())
}

func fakeOpenconnect(mode string) {
	var script string

	for i, a := range os.Args {
		if a == "--script" && i+1 < len(os.Args) {
			script = os.Args[i+1]
		}
	}

	pw, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(pw) != "secret" {
		fmt.Println("Login failed.")
		os.Exit(1)
	}

	switch mode {
	case "hang":
		time.Sleep(time.Hour)
	case "stubborn":
		signal.Ignore(syscall.SIGTERM)

		fallthrough
	case "tun":
		// What openconnect does once the tunnel is configured: run the
		// script with the connect reason and the VPN's settings, then
		// keep the tunnel up.
		cmd := exec.Command("/bin/sh", "-c", script)

		cmd.Env = append(os.Environ(), "reason=connect", "INTERNAL_IP4_DNS=10.0.0.53 10.0.0.54", "TUNDEV=utun9")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr

		if err := cmd.Run(); err != nil {
			fmt.Println("script:", err)
			os.Exit(1)
		}

		fmt.Println("Configured as 10.0.0.2, with SSL connected and DTLS disabled")

		time.Sleep(time.Hour)
	}

	_, port, _ := strings.Cut(script, " -D ")

	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	for {
		c, err := ln.Accept()
		if err != nil {
			os.Exit(1)
		}

		_ = c.Close()
	}
}

func fakeClient(t *testing.T, mode, password string) *Client {
	t.Helper()
	t.Setenv(fakeEnv, mode)

	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		openconnect: os.Args[0],
		ocproxy:     "/usr/bin/ocproxy",
		args:        []string{"https://vpn.example.com"},
		password:    password,
		ctx:         ctx,
		cancel:      cancel,
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func TestClient_GetStartsOnceAndClose(t *testing.T) {
	c := fakeClient(t, "ok", "secret")

	addr, err := c.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if c.State() != sshclient.ConnConnected {
		t.Fatalf("state=%s", c.State())
	}

	again, err := c.Get(t.Context())
	if err != nil || again != addr {
		t.Fatalf("second Get = %q, %v; want reuse of %q", again, err, addr)
	}

	p := c.proc
	_ = c.Close()

	select {
	case <-p.done:
	default:
		t.Fatal("process still running after Close")
	}

	if _, err := c.Get(t.Context()); err == nil {
		t.Fatal("Get after Close succeeded")
	}
}

func TestClient_LoginFailureCarriesOutput(t *testing.T) {
	c := fakeClient(t, "ok", "wrong")

	_, err := c.Get(t.Context())
	if err == nil || !strings.Contains(err.Error(), "Login failed") {
		t.Fatalf("err=%v, want openconnect output", err)
	}

	if c.State() != sshclient.ConnDisconnected {
		t.Fatalf("state=%s", c.State())
	}
}

func TestClient_CancelAbortsStart(t *testing.T) {
	c := fakeClient(t, "hang", "secret")

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()

	if _, err := c.Get(ctx); err == nil {
		t.Fatal("Get succeeded against a tunnel that never comes up")
	}

	if d := time.Since(start); d > stopTimeout {
		t.Fatalf("Get took %s after cancel", d)
	}
}

func TestClient_RestartsAfterExit(t *testing.T) {
	c := fakeClient(t, "ok", "secret")

	if _, err := c.Get(t.Context()); err != nil {
		t.Fatal(err)
	}

	p := c.proc
	_ = p.cmd.Process.Kill()
	<-p.done

	if _, err := c.Get(t.Context()); err != nil {
		t.Fatalf("Get after exit: %v", err)
	}

	if c.proc == p {
		t.Fatal("dead process reused")
	}
}

func TestClient_DownHoldsUntilUp(t *testing.T) {
	c := fakeClient(t, "ok", "secret")

	if _, err := c.Get(t.Context()); err != nil {
		t.Fatal(err)
	}

	p := c.proc
	c.Down()

	select {
	case <-p.done:
	default:
		t.Fatal("process still running after Down")
	}

	if c.State() != sshclient.ConnUnknown {
		t.Fatalf("state=%s after Down", c.State())
	}

	if _, err := c.Get(t.Context()); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("Get after Down: err=%v, want ErrDisconnected", err)
	}

	if err := c.Up(t.Context()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if c.State() != sshclient.ConnConnected {
		t.Fatalf("state=%s after Up", c.State())
	}
}

func TestClient_DownAbortsStart(t *testing.T) {
	c := fakeClient(t, "hang", "secret")

	errc := make(chan error, 1)

	go func() { errc <- c.Up(t.Context()) }()

	time.Sleep(300 * time.Millisecond)

	start := time.Now()

	c.Down()

	if err := <-errc; err == nil {
		t.Fatal("Up succeeded although Down aborted it")
	}

	if d := time.Since(start); d > stopTimeout+time.Second {
		t.Fatalf("Down took %s", d)
	}

	if c.State() != sshclient.ConnUnknown {
		t.Fatalf("state=%s after Down", c.State())
	}
}

func TestClient_AutoconnectOff(t *testing.T) {
	c := fakeClient(t, "ok", "secret")
	c.SetAutoconnect(false)

	if _, err := c.Get(t.Context()); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("Get with autoconnect off: err=%v, want ErrDisconnected", err)
	}

	if c.proc != nil {
		t.Fatal("Get started openconnect with autoconnect off")
	}

	if err := c.Up(t.Context()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if _, err := c.Get(t.Context()); err != nil {
		t.Fatalf("Get using running VPN: %v", err)
	}
}

func TestClient_OnDropOnlyForUnrequestedExit(t *testing.T) {
	c := fakeClient(t, "ok", "secret")

	drops := make(chan struct{}, 2)

	c.OnDrop(func() { drops <- struct{}{} })

	if _, err := c.Get(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Killed from outside: a drop.
	_ = c.proc.cmd.Process.Kill()

	select {
	case <-drops:
	case <-time.After(2 * time.Second):
		t.Fatal("no drop reported for an unrequested exit")
	}

	if _, err := c.Get(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Asked to stop: not a drop.
	c.Down()

	select {
	case <-drops:
		t.Fatal("drop reported for Down")
	case <-time.After(200 * time.Millisecond):
	}
}

// fakeSudo writes the fake sudo script into a temp dir and points its log
// env at a file there. It returns the script path and the log path.
func fakeSudo(t *testing.T) (string, string) {
	t.Helper()

	dir := t.TempDir()
	sudo := filepath.Join(dir, "sudo")
	logFile := filepath.Join(dir, "sudo.log")

	if err := os.WriteFile(sudo, []byte(fakeSudoScript), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(fakeSudoLogEnv, logFile)

	return sudo, logFile
}

func sudoLog(t *testing.T, logFile string) string {
	t.Helper()

	b, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

// fakeTunClient is fakeClient in tun mode: openconnect runs through the
// fake sudo, which logs what it is asked to run, and its vpnc-script is a
// fake that dumps its environment next to the sudo log.
func fakeTunClient(t *testing.T, mode string) (*Client, string) {
	t.Helper()

	kill, err := exec.LookPath("kill")
	if err != nil {
		t.Fatal(err)
	}

	sudo, logFile := fakeSudo(t)
	vpnc := filepath.Join(filepath.Dir(sudo), "vpnc-script")

	if err := os.WriteFile(vpnc, []byte("#!/bin/sh\nenv > \""+vpnc+".env\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := fakeClient(t, mode, "secret")
	c.ocproxy = ""
	c.tun = true
	c.sudo = sudo
	c.kill = kill
	c.vpncScript = vpnc

	return c, logFile
}

func TestClient_Tun_CommandLineAndReady(t *testing.T) {
	c, logFile := fakeTunClient(t, "tun")

	addr, err := c.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if addr != "" {
		t.Fatalf("addr=%q, want empty in tun mode", addr)
	}

	if c.State() != sshclient.ConnConnected {
		t.Fatalf("state=%s", c.State())
	}

	cmdline := sudoLog(t, logFile)
	if want := "-n " + os.Args[0] + " --script dns=$INTERNAL_IP4_DNS;"; !strings.HasPrefix(cmdline, want) {
		t.Fatalf("sudo ran %q, want prefix %q", cmdline, want)
	}

	if want := "'" + c.vpncScript + "'; rc=$?; [ \"$reason\" = connect ] && printf '%s\\n' \"$dns\" > '" + filepath.Join(c.proc.dir, "dns") + "'; exit $rc --user  https://vpn.example.com\n"; !strings.Contains(cmdline, want) {
		t.Fatalf("sudo ran %q, want %q", cmdline, want)
	}

	if c.Resolver() == nil {
		t.Fatal("Resolver() is nil, want one over the servers the script was given")
	}

	// vpnc-script ran without the nameservers, so it left the system
	// resolver alone.
	env, err := os.ReadFile(c.vpncScript + ".env")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(env), "INTERNAL_IP4_DNS") || !strings.Contains(string(env), "reason=connect") {
		t.Fatalf("vpnc-script env:\n%s", env)
	}

	c.Down()

	if c.Resolver() != nil {
		t.Fatal("Resolver() set after Down, want nil")
	}
}

func TestClient_Tun_TimeoutWithoutScript(t *testing.T) {
	c, _ := fakeTunClient(t, "hang")

	was := connectTimeout
	connectTimeout = 500 * time.Millisecond

	t.Cleanup(func() { connectTimeout = was })

	_, err := c.Get(t.Context())
	if err == nil || !strings.Contains(err.Error(), "tunnel not up after") {
		t.Fatalf("err=%v, want connect timeout", err)
	}

	if c.proc != nil {
		t.Fatal("process kept after timeout")
	}
}

func TestClient_Tun_StopSignalsThroughSudo(t *testing.T) {
	c, logFile := fakeTunClient(t, "tun")

	if _, err := c.Get(t.Context()); err != nil {
		t.Fatal(err)
	}

	p := c.proc
	pid := strconv.Itoa(p.cmd.Process.Pid)

	_ = c.Close()

	select {
	case <-p.done:
	default:
		t.Fatal("process still running after Close")
	}

	if got := sudoLog(t, logFile); !strings.Contains(got, "-n "+c.kill+" -TERM "+pid+"\n") {
		t.Fatalf("sudo log %q, want kill -TERM %s", got, pid)
	}
}

func TestClient_Tun_KillsGroupWhenTermIgnored(t *testing.T) {
	c, logFile := fakeTunClient(t, "stubborn")

	if _, err := c.Get(t.Context()); err != nil {
		t.Fatal(err)
	}

	p := c.proc
	pid := strconv.Itoa(p.cmd.Process.Pid)

	start := time.Now()

	c.Down()

	if d := time.Since(start); d > stopTimeout+time.Second {
		t.Fatalf("Down took %s", d)
	}

	select {
	case <-p.done:
	default:
		t.Fatal("process still running after Down")
	}

	if got := sudoLog(t, logFile); !strings.Contains(got, "-n "+c.kill+" -KILL -- -"+pid+"\n") {
		t.Fatalf("sudo log %q, want kill -KILL of group %s", got, pid)
	}
}

func TestNew_TunNeedsSudoers(t *testing.T) {
	sudo, _ := fakeSudo(t)
	dir := filepath.Dir(sudo)

	// New looks the binaries up on PATH; a script named openconnect that
	// answers --version is enough.
	if err := os.WriteFile(filepath.Join(dir, "openconnect"), []byte("#!/bin/sh\necho '"+vpncScriptPrefix+"/etc/vpnc/vpnc-script'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(fakeSudoDenyEnv, "1")

	u := config.Upstream{Type: config.UpstreamOpenConnect, Server: "https://vpn.example.com", Tun: true, Auth: &config.UpstreamAuth{Username: "u"}}

	_, err := New(u)
	if !errors.Is(err, ErrSudoers) || !strings.Contains(err.Error(), "dockprox tun install") {
		t.Fatalf("err=%v, want ErrSudoers with the install hint", err)
	}

	t.Setenv(fakeSudoDenyEnv, "0")

	c, err := New(u)
	if err != nil {
		t.Fatalf("New with sudoers: %v", err)
	}

	_ = c.Close()

	if !c.tun || c.sudo != sudo || c.ocproxy != "" || c.vpncScript != "/etc/vpnc/vpnc-script" {
		t.Fatalf("tun=%v sudo=%q ocproxy=%q vpncScript=%q", c.tun, c.sudo, c.ocproxy, c.vpncScript)
	}
}
