//go:build safe && darwin

package menubar

import "os/exec"

// RevealInFinder opens the macOS Finder with the given path selected.
// Returns nil if the launch was started (does not wait for completion).
func RevealInFinder(path string) error {
	return buildRevealCommand(path).Start()
}

func buildRevealCommand(path string) *exec.Cmd {
	return exec.Command("open", "-R", path)
}
