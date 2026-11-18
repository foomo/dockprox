package openconnect

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallScript(t *testing.T) {
	s := installScript("me ALL=(root) NOPASSWD: /x/it's\n")

	for _, want := range []string{
		"set -e;",
		`printf '%s' 'me ALL=(root) NOPASSWD: /x/it'\''s` + "\n'",
		`visudo -cf "$t"`,
		"install -m 0440 -o root \"$t\" " + SudoersPath,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("script %q lacks %q", s, want)
		}
	}
}

func TestAppleScriptString(t *testing.T) {
	if got, want := appleScriptString(`a "b" \c`), `"a \"b\" \\c"`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// TestInstallSudoers runs the installer against the fake sudo in no-exec
// mode, which logs what it is asked to run instead of touching /etc, and a
// no-op openconnect on PATH.
func TestInstallSudoers(t *testing.T) {
	sudo, logFile := fakeSudo(t)
	dir := filepath.Dir(sudo)

	if err := os.WriteFile(filepath.Join(dir, "openconnect"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(fakeSudoNoExecEnv, "1")

	if err := InstallSudoers(context.Background(), strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}

	// The install through sh (its argument spans two lines: the drop-in
	// content ends in a newline), then the verification of both binaries.
	calls := strings.Split(strings.TrimSpace(sudoLog(t, logFile)), "\n")
	oc, _ := lookPath("openconnect")
	_, kill, route, _ := sudoBins()

	want := []string{"-n -l " + oc, "-n -l " + kill}
	if route != "" {
		want = append(want, "-n -l "+route+" -n delete -host 192.0.2.1")
		if !strings.Contains(calls[0], route+" -n delete -host *") {
			t.Fatalf("drop-in lacks the route line: %q", calls[0])
		}
	}

	if len(calls) != 2+len(want) || !strings.HasPrefix(calls[0], "sh -c set -e;") || !strings.Contains(calls[1], SudoersPath) ||
		strings.Join(calls[2:], "\n") != strings.Join(want, "\n") {
		t.Fatalf("sudo calls: %q", calls)
	}
}
