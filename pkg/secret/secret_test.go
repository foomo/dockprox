package secret_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/foomo/dockprox/pkg/secret"
)

func TestResolve_Literal(t *testing.T) {
	got, err := secret.Resolve(context.Background(), "hunter2")
	if err != nil || got != "hunter2" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolve_Cmd(t *testing.T) {
	r := secret.New(secret.WithCmd())

	got, err := r.Resolve(context.Background(), "cmd:printf 'a b\\n'")
	if err != nil || got != "a b" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolve_CmdError(t *testing.T) {
	r := secret.New(secret.WithCmd())

	_, err := r.Resolve(context.Background(), "cmd:printf s3%s cr3t; echo nope >&2; exit 3")
	if err == nil {
		t.Fatal("want error")
	}

	if strings.Contains(err.Error(), "s3cr3t") || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error: %v", err)
	}
}

func TestResolve_CachesSuccessOnly(t *testing.T) {
	r := secret.New(secret.WithCmd())

	count := filepath.Join(t.TempDir(), "count")
	// Fails on the first run, succeeds after; each run appends a line.
	ref := "cmd:echo x >> " + count + "; [ $(wc -l < " + count + ") -gt 1 ] && printf ok"

	if _, err := r.Resolve(context.Background(), ref); err == nil {
		t.Fatal("first: want error")
	}

	for range 2 {
		if got, err := r.Resolve(context.Background(), ref); err != nil || got != "ok" {
			t.Fatalf("got %q, %v", got, err)
		}
	}

	b, _ := os.ReadFile(count)
	if n := strings.Count(string(b), "\n"); n != 2 {
		t.Fatalf("runs = %d, want 2", n)
	}
}

func TestResolve_CmdDisabledByDefault(t *testing.T) {
	_, err := secret.Resolve(context.Background(), "cmd:printf s3cr3t")
	if err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("want error without secret, got %v", err)
	}

	if err := secret.Check("cmd:printf s3cr3t"); err == nil {
		t.Fatal("Check: want error")
	}
}

func TestResolve_BadKeychain(t *testing.T) {
	if _, err := secret.Resolve(context.Background(), "keychain://svc"); err == nil {
		t.Fatal("want error")
	}
}

func TestCheck(t *testing.T) {
	for _, s := range []string{"", "hunter2"} {
		if err := secret.Check(s); err != nil {
			t.Errorf("Check(%q): %v", s, err)
		}
	}

	if err := secret.New(secret.WithCmd()).Check("cmd:anything"); err != nil {
		t.Errorf("Check(cmd:) with WithCmd: %v", err)
	}

	if err := secret.Check("keychain://svc"); err == nil {
		t.Error("bad keychain ref: want error")
	}
}

func TestCheck_MissingTool(t *testing.T) {
	t.Setenv("PATH", "")

	err := secret.Check("op://v/i/f")
	if err == nil || !strings.Contains(err.Error(), "brew install --cask 1password-cli") {
		t.Fatalf("want install hint, got %v", err)
	}
}
