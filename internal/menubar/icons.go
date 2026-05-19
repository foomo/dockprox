//go:build safe

package menubar

import _ "embed"

//go:embed icon-running.png
var iconRunning []byte

//go:embed icon-stopped.png
var iconStopped []byte

// IconRunning returns the running-state tray PNG (macOS template image).
func IconRunning() []byte { return iconRunning }

// IconStopped returns the stopped-state tray PNG (macOS template image).
// Also used when the controller is in the error state; the menu carries
// the error text.
func IconStopped() []byte { return iconStopped }
