package main

import (
	"github.com/arc53/DocsGPT-cli/cmd"

	// Must initialise before bubbletea; see the package comment.
	_ "github.com/arc53/DocsGPT-cli/internal/earlytheme"
)

func main() {
	cmd.Execute()
}
