package cmd

import (
	"github.com/arc53/DocsGPT-cli/internal/install"

	"github.com/spf13/cobra"
)

// installCmd is run by install.sh / install.ps1 to place the binary they
// unpacked, so it stays out of the help.
var installCmd = &cobra.Command{
	Use:    "install",
	Short:  "Install docsgpt-cli to your system PATH",
	Hidden: true,
	Args:   usageArgs(cobra.NoArgs),
	RunE:   func(cmd *cobra.Command, args []string) error { return install.Run() },
}
