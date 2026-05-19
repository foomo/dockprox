//go:build safe

package menubar_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/foomo/dockprox/internal/menubar"
	"github.com/foomo/dockprox/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestXDGConfigPath_UsesEnvWhenSet(t *testing.T) {
	t.Setenv("HOME", "/home/whatever")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-explicit")

	assert.Equal(t, "/tmp/xdg-explicit/dockprox/config", menubar.XDGConfigPath())
}

func TestXDGConfigPath_FallsBackToHomeConfig(t *testing.T) {
	t.Setenv("HOME", "/home/whatever")
	t.Setenv("XDG_CONFIG_HOME", "")

	assert.Equal(t, "/home/whatever/.config/dockprox/config", menubar.XDGConfigPath())
}

func TestDotfilePath(t *testing.T) {
	t.Setenv("HOME", "/home/whatever")

	assert.Equal(t, "/home/whatever/.dockprox.yaml", menubar.DotfilePath())
}

func TestLookup_PrefersXDGWhenBothExist(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	xdgPath := filepath.Join(xdg, "dockprox", "config")
	dotPath := filepath.Join(home, ".dockprox.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(xdgPath), 0o755))
	require.NoError(t, os.WriteFile(xdgPath, []byte("listen: 127.0.0.1:1\n"), 0o600))
	require.NoError(t, os.WriteFile(dotPath, []byte("listen: 127.0.0.1:2\n"), 0o600))

	path, found := menubar.Lookup()
	require.True(t, found)
	assert.Equal(t, xdgPath, path)
}

func TestLookup_FallsBackToDotfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-missing"))

	dotPath := filepath.Join(home, ".dockprox.yaml")
	require.NoError(t, os.WriteFile(dotPath, []byte("listen: 127.0.0.1:2\n"), 0o600))

	path, found := menubar.Lookup()
	require.True(t, found)
	assert.Equal(t, dotPath, path)
}

func TestLookup_NoneReturnsFalse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-missing"))

	_, found := menubar.Lookup()
	assert.False(t, found)
}

func TestBootstrap_XDGWhenEnvSet(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	path, err := menubar.Bootstrap()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(xdg, "dockprox", "config"), path)

	cfg, err := config.LoadFile(path)
	require.NoError(t, err)
	assert.Equal(t, config.Defaults().Listen, cfg.Listen)
}

func TestBootstrap_DotfileWhenXDGUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	path, err := menubar.Bootstrap()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".dockprox.yaml"), path)

	cfg, err := config.LoadFile(path)
	require.NoError(t, err)
	assert.Equal(t, config.Defaults().Listen, cfg.Listen)
}

func TestResolve_LookupHit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	dotPath := filepath.Join(home, ".dockprox.yaml")
	require.NoError(t, os.WriteFile(dotPath, []byte("listen: 127.0.0.1:2\n"), 0o600))

	path, err := menubar.Resolve()
	require.NoError(t, err)
	assert.Equal(t, dotPath, path)
}

func TestResolve_BootstrapsWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	path, err := menubar.Resolve()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".dockprox.yaml"), path)

	_, statErr := os.Stat(path)
	require.NoError(t, statErr)
}
