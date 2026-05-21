//go:build !darwin

package cli

import (
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func newMenubarCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "menubar",
		Short: "Run the macOS menu bar app (macOS only)",
		RunE: func(*cobra.Command, []string) error {
			return errors.New("menubar is only supported on macOS")
		},
	}
}
