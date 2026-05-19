//go:build safe

// Package menubar implements the macOS menu bar app that controls a
// dockprox proxy in-process.
package menubar

import (
	"os"
	"path/filepath"

	"github.com/foomo/dockprox/pkg/config"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

const (
	// DotfileName is the home-directory fallback config filename.
	DotfileName = ".dockprox.yaml"
	// XDGSubpath is the path under $XDG_CONFIG_HOME (or ~/.config).
	XDGSubpath = "dockprox/config"
)

// XDGConfigPath returns the absolute config path under XDG_CONFIG_HOME,
// falling back to $HOME/.config when XDG_CONFIG_HOME is unset.
func XDGConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, XDGSubpath)
	}

	return filepath.Join(homeDir(), ".config", XDGSubpath)
}

// DotfilePath returns $HOME/.dockprox.yaml.
func DotfilePath() string {
	return filepath.Join(homeDir(), DotfileName)
}

// Lookup probes the XDG path first, then the dotfile. Returns the first
// existing path or ("", false) if neither exists.
func Lookup() (string, bool) {
	for _, p := range []string{XDGConfigPath(), DotfilePath()} {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}

	return "", false
}

// Bootstrap creates a default config. If XDG_CONFIG_HOME is set in env,
// the file is written under $XDG_CONFIG_HOME/dockprox/config; otherwise
// to $HOME/.dockprox.yaml. Parent directories are created as needed.
func Bootstrap() (string, error) {
	var path string
	if os.Getenv("XDG_CONFIG_HOME") != "" {
		path = XDGConfigPath()
	} else {
		path = DotfilePath()
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", errors.Wrap(err, "mkdir config parent")
	}

	buf, err := yaml.Marshal(config.Defaults())
	if err != nil {
		return "", errors.Wrap(err, "marshal defaults")
	}

	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return "", errors.Wrap(err, "write config")
	}

	return path, nil
}

// Resolve returns an existing config path or bootstraps a default.
func Resolve() (string, error) {
	if p, ok := Lookup(); ok {
		return p, nil
	}

	return Bootstrap()
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}

	h, _ := os.UserHomeDir()

	return h
}
