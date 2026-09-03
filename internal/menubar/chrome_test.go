//go:build darwin

package menubar //nolint:testpackage // needs access to unexported buildChromeCommand

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildChromeCommand_Defaults(t *testing.T) {
	cmd := buildChromeCommand("127.0.0.1:8888", "/tmp/profile", nil)

	// Must go through `open`, never a path inside Chrome's bundle: exec'ing
	// that is what triggers the macOS App Management permission prompt.
	assert.Equal(t, "open", filepath.Base(cmd.Path))
	assert.Equal(t, []string{
		"open", "-n", "-a", "Google Chrome", "--args",
		"--window-name=dockprox",
		"--proxy-server=http://127.0.0.1:8888",
		"--proxy-bypass-list=<-loopback>",
		"--user-data-dir=/tmp/profile",
		"--no-first-run",
		"--no-default-browser-check",
	}, cmd.Args)
}

// -n is what makes the launch a separate instance; without it LaunchServices
// activates a running Chrome and discards the proxy and profile flags.
func TestBuildChromeCommand_ForcesNewInstance(t *testing.T) {
	cmd := buildChromeCommand("127.0.0.1:8888", "/tmp/profile", nil)

	assert.Equal(t, "-n", cmd.Args[1])
	assert.Contains(t, cmd.Args, "--args")
	// Chrome's own flags must all land after --args.
	argsIdx := slices.Index(cmd.Args, "--args")
	for _, a := range cmd.Args[:argsIdx] {
		assert.NotContains(t, a, "--proxy-server")
	}
}

func TestBuildChromeCommand_ConfigOverrides(t *testing.T) {
	cmd := buildChromeCommand("127.0.0.1:8888", "/tmp/profile", &config.Chrome{
		App:   "Chromium",
		Flags: []string{"--incognito", "--window-size=1280,800"},
	})

	assert.Equal(t, []string{"-n", "-a", "Chromium"}, cmd.Args[1:4])
	// Config flags land last, so they win over the defaults above.
	assert.Equal(t, []string{"--incognito", "--window-size=1280,800"}, cmd.Args[len(cmd.Args)-2:])
}

func TestBuildChromeCommand_AppSelector(t *testing.T) {
	for _, tc := range []struct {
		name string
		app  string
		want string
	}{
		{"app name", "Google Chrome", "-a"},
		{"app path", "/Applications/Chromium.app", "-a"},
		{"bundle id", "com.google.Chrome", "-b"},
		{"dotted app bundle", "Brave Browser.app", "-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := buildChromeCommand("127.0.0.1:8888", "/tmp/p", &config.Chrome{App: tc.app})

			assert.Equal(t, tc.want, cmd.Args[2])
			assert.Equal(t, tc.app, cmd.Args[3])
		})
	}
}

// The launch deadline must not become a lifetime cap on the browser, so the
// command carries no context of its own.
func TestBuildChromeCommand_NoContextCancellation(t *testing.T) {
	cmd := buildChromeCommand("127.0.0.1:8888", "/tmp/profile", nil)

	assert.Nil(t, cmd.Cancel, "command must not be killed when the launch ctx expires")
}

func TestLaunchChrome_RequiresListenAddr(t *testing.T) {
	err := LaunchChrome(t.Context(), "", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "proxy not listening")
}

func TestLaunchChrome_HonorsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := LaunchChrome(ctx, "127.0.0.1:8888", nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
