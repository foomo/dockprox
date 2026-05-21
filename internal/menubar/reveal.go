//go:build darwin

package menubar

import (
	"context"
	"os/exec"
)

// RevealInFinder opens the macOS Finder with the given path selected.
// Returns nil if the launch was started (does not wait for completion).
func RevealInFinder(ctx context.Context, path string) error {
	return buildRevealCommand(ctx, path).Start()
}

func buildRevealCommand(ctx context.Context, path string) *exec.Cmd {
	return exec.CommandContext(ctx, "open", "-R", path)
}
