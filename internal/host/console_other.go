//go:build !windows

package host

// DetachConsole is a no-op outside Windows; systemd and launchd services
// have no console window to shed.
func DetachConsole() {}
