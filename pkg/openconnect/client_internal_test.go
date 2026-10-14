package openconnect

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/foomo/dockprox/pkg/sshclient"
)

// fakeEnv switches the test binary into a fake openconnect: it checks the
// password on stdin, then listens where ocproxy would. Values: "ok", "hang"
// (never comes up).
const fakeEnv = "DOCKPROX_FAKE_OPENCONNECT"

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

	if mode == "hang" {
		select {}
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
