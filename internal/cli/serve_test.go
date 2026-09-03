package cli

import (
	"strings"
	"testing"

	"github.com/foomo/dockprox/pkg/config"
)

func TestParseUpstreamFlag_SSH(t *testing.T) {
	name, u, err := parseUpstreamFlag("jump=ssh://bastion.example.com:2222")
	if err != nil {
		t.Fatal(err)
	}

	if name != "jump" {
		t.Fatalf("name=%q", name)
	}

	if u.Type != config.UpstreamSSH {
		t.Fatalf("type=%q", u.Type)
	}

	if u.Host != "bastion.example.com" {
		t.Fatalf("host=%q", u.Host)
	}

	if u.Port != 2222 {
		t.Fatalf("port=%d", u.Port)
	}
}

func TestParseUpstreamFlag_SSHNoPort(t *testing.T) {
	_, u, err := parseUpstreamFlag("jump=ssh://bastion.example.com")
	if err != nil {
		t.Fatal(err)
	}

	if u.Host != "bastion.example.com" || u.Port != 0 {
		t.Fatalf("host=%q port=%d, want host set and port unset", u.Host, u.Port)
	}
}

func TestParseUpstreamFlag_Forward(t *testing.T) {
	name, u, err := parseUpstreamFlag("cluster-a=forward://127.0.0.1:10310")
	if err != nil {
		t.Fatal(err)
	}

	if name != "cluster-a" {
		t.Fatalf("name=%q", name)
	}

	if u.Type != config.UpstreamForward {
		t.Fatalf("type=%q", u.Type)
	}

	if u.Addr != "127.0.0.1:10310" {
		t.Fatalf("addr=%q", u.Addr)
	}
}

func TestParseUpstreamFlag_SSHBadPort(t *testing.T) {
	_, _, err := parseUpstreamFlag("jump=ssh://bastion.example.com:notaport")
	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("err=%v want substring %q", err, "port")
	}
}
