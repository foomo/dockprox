// Package cli wires the dockprox cobra command tree.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
)

var (
	version        = "dev"
	commitHash     = "none"
	buildTimestamp = "unknown"
)

// NewRoot returns the root cobra command. Exported so generators can
// reflect over the same command tree as the binary.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "dockprox",
		Short: "Inverse Docker proxy with SOCKS5 support",
		Long: "dockprox is a local HTTP(S) proxy that bypasses traffic by default " +
			"and routes only matched domains through named upstreams (SOCKS5 or HTTP).",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(newServeCmd(), newVersionCmd())

	return root
}

// Execute runs the CLI under fang's styled UX.
func Execute(ctx context.Context) error {
	if err := fang.Execute(ctx, NewRoot()); err != nil { //nolint:contextcheck // fang.Execute already takes ctx
		fmt.Fprintln(os.Stderr, err)
		return err
	}

	return nil
}
