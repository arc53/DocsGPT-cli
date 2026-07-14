package host

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// WindowsTaskName is the Task Scheduler task name for the host daemon,
// mirroring ServiceUnitName (systemd) and LaunchdLabel (launchd).
const WindowsTaskName = "docsgpt-cli-host"

// IsWindows reports whether the current platform is Windows.
func IsWindows() bool { return runtime.GOOS == "windows" }

// WindowsTaskXMLPath returns where the rendered task definition lives
// (~/.docsgpt/docsgpt-cli-host.task.xml). The file stays on disk after
// install so a failed `schtasks /Create` can be retried by hand.
func WindowsTaskXMLPath() string {
	return filepath.Join(hostConfigDir(), WindowsTaskName+".task.xml")
}

// xmlEscape escapes a string for use as XML character data or an
// attribute value (paths may contain '&', usernames '<' in theory).
func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// RenderWindowsTaskXML returns the Task Scheduler task definition for the
// host daemon: run at logon as ``runUser``, restart on failure, no run-time
// limit. The action invokes ``exec host --service`` so the daemon drops its
// console window and appends to the host log file instead of a terminal.
//
// Two settings depart from Task Scheduler defaults on purpose:
// ExecutionTimeLimit PT0S (the default PT72H would kill the daemon after
// three days) and the battery settings (a laptop host should keep running
// unplugged). RestartOnFailure mirrors systemd Restart=on-failure; a revoke
// exits 0, which Task Scheduler does not treat as a failure, so a revoked
// device stays stopped.
func RenderWindowsTaskXML(execPath, runUser string) string {
	exe := xmlEscape(execPath)
	user := xmlEscape(runUser)

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">` + "\n")
	b.WriteString("  <RegistrationInfo>\n")
	b.WriteString("    <Description>DocsGPT CLI host daemon</Description>\n")
	b.WriteString("  </RegistrationInfo>\n")
	b.WriteString("  <Triggers>\n")
	b.WriteString("    <LogonTrigger>\n")
	b.WriteString("      <Enabled>true</Enabled>\n")
	fmt.Fprintf(&b, "      <UserId>%s</UserId>\n", user)
	b.WriteString("    </LogonTrigger>\n")
	b.WriteString("  </Triggers>\n")
	b.WriteString("  <Principals>\n")
	b.WriteString(`    <Principal id="Author">` + "\n")
	fmt.Fprintf(&b, "      <UserId>%s</UserId>\n", user)
	// InteractiveToken + LeastPrivilege: runs as the logged-in user with no
	// elevation, which is what lets a non-administrator install the task.
	b.WriteString("      <LogonType>InteractiveToken</LogonType>\n")
	b.WriteString("      <RunLevel>LeastPrivilege</RunLevel>\n")
	b.WriteString("    </Principal>\n")
	b.WriteString("  </Principals>\n")
	b.WriteString("  <Settings>\n")
	b.WriteString("    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>\n")
	b.WriteString("    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>\n")
	b.WriteString("    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>\n")
	b.WriteString("    <AllowHardTerminate>true</AllowHardTerminate>\n")
	b.WriteString("    <StartWhenAvailable>true</StartWhenAvailable>\n")
	b.WriteString("    <Enabled>true</Enabled>\n")
	b.WriteString("    <Hidden>false</Hidden>\n")
	b.WriteString("    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>\n")
	b.WriteString("    <RestartOnFailure>\n")
	b.WriteString("      <Interval>PT1M</Interval>\n")
	b.WriteString("      <Count>10</Count>\n")
	b.WriteString("    </RestartOnFailure>\n")
	b.WriteString("  </Settings>\n")
	b.WriteString(`  <Actions Context="Author">` + "\n")
	b.WriteString("    <Exec>\n")
	fmt.Fprintf(&b, "      <Command>%s</Command>\n", exe)
	b.WriteString("      <Arguments>host --service</Arguments>\n")
	b.WriteString("    </Exec>\n")
	b.WriteString("  </Actions>\n")
	b.WriteString("</Task>\n")
	return b.String()
}

// WriteWindowsTaskXML writes the task definition at ``path``, creating the
// parent directory (0700 — it is ~/.docsgpt) when missing.
func WriteWindowsTaskXML(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("mkdir task xml dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write task xml: %w", err)
	}
	return nil
}

// runSchtasks shells out to schtasks.exe, returning combined output on
// failure so the caller can pass it through to the user.
func runSchtasks(args ...string) error {
	cmd := exec.Command("schtasks", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks %s: %s",
			strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// RegisterWindowsTask creates (or replaces) the scheduled task from the
// XML definition at ``xmlPath``.
func RegisterWindowsTask(xmlPath string) error {
	return runSchtasks("/Create", "/TN", WindowsTaskName, "/XML", xmlPath, "/F")
}

// StartWindowsTask starts the scheduled task immediately (the logon trigger
// only fires on the next logon).
func StartWindowsTask() error {
	return runSchtasks("/Run", "/TN", WindowsTaskName)
}

// EndWindowsTask stops a running instance of the task. Failing is normal
// when no instance is running.
func EndWindowsTask() error {
	return runSchtasks("/End", "/TN", WindowsTaskName)
}

// DeleteWindowsTask removes the scheduled task registration.
func DeleteWindowsTask() error {
	return runSchtasks("/Delete", "/TN", WindowsTaskName, "/F")
}

// WindowsTaskInstalled reports whether the scheduled task is registered.
func WindowsTaskInstalled() bool {
	return runSchtasks("/Query", "/TN", WindowsTaskName) == nil
}

// WindowsTaskRegisterCmd returns the manual registration command string,
// shown to the user when the automatic `schtasks /Create` fails.
func WindowsTaskRegisterCmd(xmlPath string) string {
	return fmt.Sprintf(`schtasks /Create /TN %s /XML "%s" /F`,
		WindowsTaskName, xmlPath)
}
