package sshclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testSigner(t *testing.T) ssh.Signer {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func TestHostKeyCallback_Pinned(t *testing.T) {
	good := testSigner(t)
	other := testSigner(t)

	tgt := &Target{Host: "b.example.com", Port: 22, HostKey: ssh.FingerprintSHA256(good.PublicKey())}

	cb, err := tgt.hostKeyCallback()
	if err != nil {
		t.Fatal(err)
	}

	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}

	if err := cb("b.example.com:22", addr, good.PublicKey()); err != nil {
		t.Fatalf("matching key rejected: %v", err)
	}

	err = cb("b.example.com:22", addr, other.PublicKey())
	if err == nil {
		t.Fatal("mismatched key accepted")
	}

	if !strings.Contains(err.Error(), "SHA256:") {
		t.Fatalf("error should name both fingerprints: %v", err)
	}
}
