package openconnect

import (
	"context"
	"io"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

// SudoersPath is the drop-in tun mode installs. A drop-in rather than an
// edit to /etc/sudoers: visudo validates it on its own, and removing the
// file undoes the setup.
const SudoersPath = "/etc/sudoers.d/dockprox"

// Sudoers returns the drop-in content tun mode needs: passwordless root
// for openconnect (to start the VPN) and kill (to stop it) for the current
// user. Note that kill is unrestricted, so this user can then signal any
// root process without a password.
func Sudoers() (string, error) {
	oc, err := lookPath("openconnect")
	if err != nil {
		return "", err
	}

	_, kill, err := sudoBins()
	if err != nil {
		return "", err
	}

	me, err := user.Current()
	if err != nil {
		return "", errors.Wrap(err, "current user")
	}

	return me.Username + " ALL=(root) NOPASSWD: " + oc + ", " + kill + "\n", nil
}

// InstallSudoers writes the drop-in through an interactive sudo, which
// prompts for the password on the terminal.
func InstallSudoers(ctx context.Context, stdin io.Reader, stderr io.Writer) error {
	return install(ctx, func(sudo, script string) error {
		cmd := exec.CommandContext(ctx, sudo, "sh", "-c", script)
		cmd.Stdin = stdin
		cmd.Stdout = stderr
		cmd.Stderr = stderr

		return cmd.Run()
	})
}

// InstallSudoersGUI writes the drop-in through the macOS authorization
// dialog (osascript's "with administrator privileges"), for the menu bar
// app, which has no terminal to prompt on.
func InstallSudoersGUI(ctx context.Context) error {
	return install(ctx, func(_, script string) error {
		out, err := exec.CommandContext(ctx, "osascript", "-e", //nolint:gosec // script built from lookPath results and the current username
			"do shell script "+appleScriptString(script)+" with administrator privileges").CombinedOutput()
		if err != nil {
			return errors.Wrap(err, strings.TrimSpace(string(out)))
		}

		return nil
	})
}

// install writes the drop-in with run, which executes a shell script as
// root, then verifies that sudo no longer asks for a password, so an
// install that landed but has no effect (sudoers without an includedir
// line) is reported here rather than at the next start.
func install(ctx context.Context, run func(sudo, script string) error) error {
	content, err := Sudoers()
	if err != nil {
		return err
	}

	oc, _ := lookPath("openconnect")
	sudo, kill, _ := sudoBins()

	if err := run(sudo, installScript(content)); err != nil {
		return errors.Wrap(err, "install sudoers")
	}

	if err := checkSudo(ctx, sudo, oc, kill); err != nil {
		return errors.Errorf("%s is installed but sudo still asks for a password: /etc/sudoers needs an \"@includedir %s\" line", SudoersPath, filepath.Dir(SudoersPath))
	}

	return nil
}

// installScript is the shell that writes content to SudoersPath as root.
// visudo checks the file before it lands, so a bad line cannot break sudo.
func installScript(content string) string {
	dir := filepath.Dir(SudoersPath)

	return "set -e; t=$(mktemp); printf '%s' " + shellQuote(content) + " > \"$t\"; " +
		"visudo -cf \"$t\" >/dev/null; mkdir -p " + dir + "; " +
		"install -m 0440 -o root \"$t\" " + SudoersPath + "; rm -f \"$t\""
}

// shellQuote single-quotes s for sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appleScriptString double-quotes s as an AppleScript string literal.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)

	return `"` + s + `"`
}
