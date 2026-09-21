package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"docsgpt-cli/internal/display"

	"github.com/spf13/cobra"
)

// noModifyPathEnv mirrors the installer scripts: set it to 1 and we report what
// to add to PATH instead of touching a shell profile.
const noModifyPathEnv = "DOCSGPT_NO_MODIFY_PATH"

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install docsgpt-cli to your system PATH",
	// install.sh and install.ps1 run this to place the binary they unpacked, so
	// a failure has to exit non-zero and must not bury the reason in usage text.
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		binaryName := "docsgpt-cli"
		if runtime.GOOS == "windows" {
			binaryName += ".exe"
		}
		sourcePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("could not determine the running executable: %w", err)
		}
		// A symlinked binary would otherwise be "moved" by relinking the link.
		if resolved, err := filepath.EvalSymlinks(sourcePath); err == nil {
			sourcePath = resolved
		}

		installDir := getInstallDir()
		if installDir == "" {
			return fmt.Errorf("no installation directory is known for %s", runtime.GOOS)
		}
		destinationPath := filepath.Join(installDir, binaryName)

		if err := os.MkdirAll(installDir, 0755); err != nil {
			return fmt.Errorf("could not create %s: %w", installDir, err)
		}

		if sameFile(sourcePath, destinationPath) {
			fmt.Println(display.Success("docsgpt-cli is already installed at " + destinationPath))
		} else {
			if err := placeBinary(sourcePath, destinationPath); err != nil {
				return err
			}
			fmt.Println(display.Success("docsgpt-cli installed to " + destinationPath))
		}

		if runtime.GOOS == "windows" {
			if onPath(installDir) {
				return nil
			}
			if os.Getenv(noModifyPathEnv) == "1" {
				fmt.Println(display.Muted("Add " + installDir + " to your PATH to run docsgpt-cli by name."))
				return nil
			}
			if err := addToWindowsPATH(installDir); err != nil {
				return fmt.Errorf("could not add %s to PATH: %w", installDir, err)
			}
			fmt.Println(display.Muted("Added " + installDir + " to your PATH. Open a new terminal to pick it up."))
			return nil
		}

		ensureOnPath(installDir)
		return nil
	},
}

// placeBinary moves src onto dst, falling back to a copy across filesystems
// (the installers unpack into a temp dir, which is often a different mount).
func placeBinary(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		// Rename keeps the source mode; make sure it is executable either way.
		if err := os.Chmod(dst, 0755); err != nil {
			return fmt.Errorf("could not make %s executable: %w", dst, err)
		}
		return nil
	}
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("could not install to %s: %w", dst, err)
	}
	return nil
}

// getInstallDir picks the directory to install into: a system-wide bin when we
// can write to it, otherwise a per-user one that never needs elevation.
func getInstallDir() string {
	switch runtime.GOOS {
	case "linux", "darwin":
		if isWritable("/usr/local/bin") {
			return "/usr/local/bin"
		}
		return filepath.Join(os.Getenv("HOME"), ".local", "bin")
	case "windows":
		// Per-user directory; created by the caller if missing, no elevation needed.
		return filepath.Join(os.Getenv("USERPROFILE"), "bin")
	default:
		return ""
	}
}

func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

func isWritable(dir string) bool {
	testFile := filepath.Join(dir, ".docsgpt-write-test")
	if err := os.WriteFile(testFile, []byte{}, 0644); err != nil {
		return false
	}
	os.Remove(testFile)
	return true
}

// onPath reports whether dir is already one of the PATH entries.
func onPath(dir string) bool {
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if entry == "" {
			continue
		}
		if entry == dir {
			return true
		}
		// Tolerate a trailing separator and relative spellings of the same dir.
		if abs, err := filepath.Abs(entry); err == nil && abs == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// ensureOnPath makes the installed binary reachable by name from new shells.
// Failing to edit a profile is reported, not fatal: the binary is installed
// and the user can add the directory themselves.
func ensureOnPath(dir string) {
	if onPath(dir) {
		return
	}
	if os.Getenv(noModifyPathEnv) == "1" {
		fmt.Println(display.Muted("Add " + dir + " to your PATH to run docsgpt-cli by name."))
		return
	}
	configPath, entry, err := shellPathEntry(dir)
	if err != nil {
		fmt.Println(display.Muted("Add " + dir + " to your PATH to run docsgpt-cli by name."))
		return
	}
	added, err := appendToShellConfig(configPath, entry)
	if err != nil {
		fmt.Println(display.Muted("Could not update " + configPath + ": " + err.Error()))
		fmt.Println(display.Muted("Add " + dir + " to your PATH to run docsgpt-cli by name."))
		return
	}
	if added {
		fmt.Println(display.Muted("Added " + dir + " to your PATH in " + configPath + ". Open a new terminal to pick it up."))
		return
	}
	fmt.Println(display.Muted(configPath + " already adds " + dir + " to PATH. Open a new terminal to pick it up."))
}

// shellPathEntry returns the profile to edit and the line that puts dir on PATH
// in that shell's syntax.
func shellPathEntry(dir string) (configPath, entry string, err error) {
	home := os.Getenv("HOME")
	shell := os.Getenv("SHELL")
	// Keep the profile portable when the directory lives under $HOME.
	quoted := dir
	if home != "" && strings.HasPrefix(dir, home+string(os.PathSeparator)) {
		quoted = "$HOME" + strings.TrimPrefix(dir, home)
	}

	switch {
	case strings.Contains(shell, "fish"):
		// fish_add_path is idempotent, so re-running install is harmless.
		return filepath.Join(home, ".config", "fish", "config.fish"),
			"fish_add_path " + dir, nil
	case strings.Contains(shell, "zsh"):
		return filepath.Join(home, ".zshrc"),
			`export PATH="` + quoted + `:$PATH"`, nil
	case strings.Contains(shell, "bash"):
		return filepath.Join(home, ".bashrc"),
			`export PATH="` + quoted + `:$PATH"`, nil
	}
	return "", "", errors.New("unrecognised shell")
}

func addToWindowsPATH(dir string) error {
	// setx is unusable here: %PATH% is not expanded outside cmd.exe, it merges
	// the machine PATH into the user PATH, and it truncates values to 1024
	// characters. Update only the user PATH via PowerShell instead.
	script := fmt.Sprintf(
		"$dir = '%s';"+
			"$path = [Environment]::GetEnvironmentVariable('Path', 'User');"+
			"if ($null -eq $path) { $path = '' };"+
			"$parts = $path -split ';' | Where-Object { $_ -ne '' };"+
			"if ($parts -notcontains $dir) {"+
			"[Environment]::SetEnvironmentVariable('Path', (($parts + $dir) -join ';'), 'User')"+
			"}",
		strings.ReplaceAll(dir, "'", "''"),
	)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	return cmd.Run()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// appendToShellConfig adds entry to configPath unless it is already there.
// It reports whether the file was changed.
func appendToShellConfig(configPath, entry string) (bool, error) {
	existing, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == entry {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return false, err
	}
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return false, err
	}
	defer file.Close()

	prefix := "\n"
	if len(existing) == 0 || strings.HasSuffix(string(existing), "\n") {
		prefix = ""
	}
	if _, err := file.WriteString(prefix + "# Added by docsgpt-cli install\n" + entry + "\n"); err != nil {
		return false, err
	}
	return true, file.Close()
}
