package menubar

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
)

// settingsSubpath is the app's own preferences file under the user's
// Application Support dir, next to the Chrome profile. It holds per-machine
// toggles that do not belong in the shareable YAML config.
const settingsSubpath = "dockprox/settings.json"

// Settings are the menu bar app's persisted preferences.
type Settings struct {
	// Autoconnect opens ssh and openconnect connections on demand when a
	// request needs them. Nil means unset, which is on; see AutoconnectOn.
	Autoconnect *bool `json:"autoconnect,omitempty"`
}

// AutoconnectOn returns Autoconnect, defaulting to on.
func (s Settings) AutoconnectOn() bool {
	return s.Autoconnect == nil || *s.Autoconnect
}

// SettingsPath returns ~/Library/Application Support/dockprox/settings.json.
func SettingsPath() string {
	return filepath.Join(homeDir(), "Library", "Application Support", settingsSubpath)
}

// LoadSettings reads path. A missing file yields the zero Settings.
func LoadSettings(path string) (Settings, error) {
	var s Settings

	buf, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}

	if err != nil {
		return s, errors.Wrap(err, "read settings")
	}

	return s, errors.Wrap(json.Unmarshal(buf, &s), "parse settings")
}

// SaveSettings writes s to path, creating its directory.
func SaveSettings(path string, s Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errors.Wrap(err, "settings dir")
	}

	buf, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return errors.Wrap(err, "encode settings")
	}

	return errors.Wrap(os.WriteFile(path, append(buf, '\n'), 0o600), "write settings")
}
