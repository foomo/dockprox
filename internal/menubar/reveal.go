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

// releasesURL is the GitHub releases page for this project.
const releasesURL = "https://github.com/foomo/dockprox/releases"

// openReleases opens the GitHub releases page in the default browser.
// Returns nil if the launch was started (does not wait for completion).
func openReleases() error {
	return exec.Command("open", releasesURL).Start() //nolint:gosec // fixed constant URL, not user input
}
