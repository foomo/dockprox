package cli

import (
	"fmt"

	"github.com/foomo/dockprox/pkg/openconnect"
	"github.com/spf13/cobra"
)

func newTunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tun",
		Short: "Set up kernel tun mode for openconnect upstreams",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "install",
		Short: "Install the sudoers drop-in that tun mode needs",
		Long: "Writes " + openconnect.SudoersPath + " so that openconnect (to start the VPN) " +
			"and kill (to stop it) run as root without a password for the current user. " +
			"sudo asks for your password once. Delete the file to undo.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			content, err := openconnect.Sudoers()
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "writing %s:\n  %s", openconnect.SudoersPath, content)

			if err := openconnect.InstallSudoers(cmd.Context(), cmd.InOrStdin(), cmd.ErrOrStderr()); err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), "installed", openconnect.SudoersPath)

			return nil
		},
	})

	return cmd
}
