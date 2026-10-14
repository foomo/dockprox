package menubar_test

import (
	"path/filepath"
	"testing"

	"github.com/foomo/dockprox/internal/menubar"
	"github.com/stretchr/testify/require"
)

func TestSettings_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")

	s, err := menubar.LoadSettings(path)
	require.NoError(t, err, "missing file is not an error")
	require.True(t, s.AutoconnectOn(), "autoconnect defaults to on")

	off := false
	require.NoError(t, menubar.SaveSettings(path, menubar.Settings{Autoconnect: &off}))

	s, err = menubar.LoadSettings(path)
	require.NoError(t, err)
	require.False(t, s.AutoconnectOn())
}
