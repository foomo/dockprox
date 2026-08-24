package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/foomo/dockprox/pkg/config"
)

func TestDefaultLogPath_UsesXDGCacheHomeWhenSet(t *testing.T) {
	t.Setenv("HOME", "/home/whatever")
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache-explicit")

	path, err := config.DefaultLogPath()
	if err != nil {
		t.Fatal(err)
	}

	if want := "/tmp/xdg-cache-explicit/dockprox/dockprox.log"; path != want {
		t.Errorf("got %q, want %q", path, want)
	}
}

func TestDefaultLogPath_FallsBackToHomeCache(t *testing.T) {
	t.Setenv("HOME", "/home/whatever")
	t.Setenv("XDG_CACHE_HOME", "")

	path, err := config.DefaultLogPath()
	if err != nil {
		t.Fatal(err)
	}

	if want := "/home/whatever/.cache/dockprox/dockprox.log"; path != want {
		t.Errorf("got %q, want %q", path, want)
	}
}

func TestResolveLogPath_DefaultsWhenConfigEmpty(t *testing.T) {
	t.Setenv("HOME", "/home/whatever")
	t.Setenv("XDG_CACHE_HOME", "")

	path, err := config.ResolveLogPath(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}

	if want := "/home/whatever/.cache/dockprox/dockprox.log"; path != want {
		t.Errorf("got %q, want %q", path, want)
	}
}

func TestResolveLogPath_UsesConfigOverride(t *testing.T) {
	path, err := config.ResolveLogPath(&config.Config{LogFile: "/custom/path/dockprox.log"})
	if err != nil {
		t.Fatal(err)
	}

	if want := "/custom/path/dockprox.log"; path != want {
		t.Errorf("got %q, want %q", path, want)
	}
}

func TestOpenLogWriter_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "nested", "dockprox.log")

	w, err := config.OpenLogWriter(&config.Config{LogFile: logFile})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(logFile); err != nil {
		t.Errorf("expected %s to exist: %v", logFile, err)
	}
}

func TestOpenLogWriter_CreatesFileBeforeFirstWrite(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "dockprox.log")

	if _, err := config.OpenLogWriter(&config.Config{LogFile: logFile}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(logFile); err != nil {
		t.Errorf("expected %s to exist: %v", logFile, err)
	}
}

func TestLevelFromString(t *testing.T) {
	cases := map[string]string{
		"":      "info",
		"debug": "debug",
		"info":  "info",
		"warn":  "warn",
		"error": "error",
		"bogus": "info",
	}

	for in, want := range cases {
		if got := config.LevelFromString(in).String(); got != want {
			t.Errorf("LevelFromString(%q) = %q, want %q", in, got, want)
		}
	}
}
