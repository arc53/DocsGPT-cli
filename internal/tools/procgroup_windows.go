package tools

import "os/exec"

// killProcessGroup keeps the default: cancellation kills the process itself.
func killProcessGroup(*exec.Cmd) {}
