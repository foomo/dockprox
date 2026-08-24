package sshclient_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/foomo/dockprox/pkg/sshclient"
	"golang.org/x/crypto/ssh"
)

// writeKey generates an ed25519 private key at dir/name, encrypted with
// passphrase when it is non-empty, and returns the path.
func writeKey(t *testing.T, dir, name, passphrase string) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}

	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestNew_Defaults(t *testing.T) {
	dir := t.TempDir()
	key := writeKey(t, dir, "id", "")

	tgt, err := sshclient.New(config.Upstream{Type: "ssh", Host: "b.example.com", KeyFile: key})
	if err != nil {
		t.Fatal(err)
	}

	if tgt.Port != 22 {
		t.Fatalf("Port=%d, want 22", tgt.Port)
	}

	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}

	if tgt.User != me.Username {
		t.Fatalf("User=%q, want %q", tgt.User, me.Username)
	}

	if got := tgt.Addr(); got != "b.example.com:22" {
		t.Fatalf("Addr=%q", got)
	}
}

func TestNew_ExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeKey(t, home, "id", "")

	tgt, err := sshclient.New(config.Upstream{Type: "ssh", Host: "b", KeyFile: "~/id"})
	if err != nil {
		t.Fatal(err)
	}

	if tgt.KeyFile != filepath.Join(home, "id") {
		t.Fatalf("KeyFile=%q, want %q", tgt.KeyFile, filepath.Join(home, "id"))
	}
}

func TestValidate(t *testing.T) {
	dir := t.TempDir()
	plain := writeKey(t, dir, "plain", "")
	enc := writeKey(t, dir, "enc", "s3cret")

	sock := filepath.Join(dir, "agent.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// hostKey pinning needs a valid fingerprint; generate one.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	fp := ssh.FingerprintSHA256(signer.PublicKey())

	cases := []struct {
		name string
		u    config.Upstream
		msg  string // empty means "expect success"
	}{
		{"unencrypted key", config.Upstream{Host: "b", KeyFile: plain, HostKey: fp}, ""},
		{"encrypted key with passphrase", config.Upstream{
			Host: "b", KeyFile: enc, KeyFilePassphrase: "s3cret", HostKey: fp,
		}, ""},
		{"encrypted key no passphrase", config.Upstream{
			Host: "b", KeyFile: enc, HostKey: fp,
		}, "encrypted"},
		{"encrypted key no passphrase but agent", config.Upstream{
			Host: "b", KeyFile: enc, IdentityAgent: sock, HostKey: fp,
		}, ""},
		{"missing key file", config.Upstream{
			Host: "b", KeyFile: filepath.Join(dir, "nope"), HostKey: fp,
		}, "keyFile"},
		{"explicit agent socket", config.Upstream{
			Host: "b", IdentityAgent: sock, HostKey: fp,
		}, ""},
		{"missing agent socket", config.Upstream{
			Host: "b", IdentityAgent: filepath.Join(dir, "nope.sock"), HostKey: fp,
		}, "identityAgent"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.u.Type = "ssh"

			tgt, err := sshclient.New(tc.u)
			if err != nil {
				t.Fatal(err)
			}

			err = tgt.Validate()
			switch {
			case tc.msg == "" && err != nil:
				t.Fatalf("Validate: %v", err)
			case tc.msg != "" && (err == nil || !strings.Contains(err.Error(), tc.msg)):
				t.Fatalf("err=%v want substring %q", err, tc.msg)
			}
		})
	}
}

func TestValidate_AgentSentinel(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "agent.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	u := config.Upstream{Type: "ssh", Host: "b", IdentityAgent: "SSH_AUTH_SOCK", HostKey: "SHA256:" + strings.Repeat("A", 43)}

	t.Run("env set", func(t *testing.T) {
		t.Setenv("SSH_AUTH_SOCK", sock)

		tgt, err := sshclient.New(u)
		if err != nil {
			t.Fatal(err)
		}

		if err := tgt.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})

	t.Run("env unset", func(t *testing.T) {
		t.Setenv("SSH_AUTH_SOCK", "")

		tgt, err := sshclient.New(u)
		if err != nil {
			t.Fatal(err)
		}

		err = tgt.Validate()
		if err == nil || !strings.Contains(err.Error(), "SSH_AUTH_SOCK") {
			t.Fatalf("err=%v want SSH_AUTH_SOCK", err)
		}
	})
}
