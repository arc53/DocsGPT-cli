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
	"docsgpt-cli/internal/update"

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

		// Under Intel Homebrew /usr/local/bin is user-writable and holds a
		// symlink into the Caskroom, so without this we would install straight
		// through it and fight `brew upgrade` over the same binary.
		if resolved, err := filepath.EvalSymlinks(destinationPath); err == nil &&
			update.IsHomebrewPath(resolved) {
			return fmt.Errorf(
				"%s is managed by Homebrew (%s). Upgrade it with: brew upgrade --cask docsgpt-cli",
				destinationPath, resolved)
		}

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

// placeBinary puts src at dst, replacing whatever is there.
//
// The copy always goes to a temporary file in the destination directory and is
// renamed into place, so an interrupted or short write cannot leave a truncated
// binary where a working one used to be, and a dst that is a symlink (an Intel
// Homebrew bin entry, say) is replaced rather than written through. A plain
// rename of src is tried first: it is the same operation when the installers
// unpack onto the same filesystem.
func placeBinary(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		// Rename keeps the source mode; make sure it is executable either way.
		if err := os.Chmod(dst, 0755); err != nil {
			return fmt.Errorf("could not make %s executable: %w", dst, err)
		}
		return nil
	}

	staged, err := copyToTemp(src, dst)
	if err != nil {
		return fmt.Errorf("could not install to %s: %w", dst, err)
	}
	if err := os.Rename(staged, dst); err != nil {
		// Windows refuses to replace a file that is currently executing, so
		// move the old one aside and retry. It stays on disk as .old until the
		// next install overwrites it; nothing else can remove a running binary.
		// On Unix a rename over a running binary works, so this is not needed
		// there and would only leave strays behind for unrelated failures.
		if runtime.GOOS == "windows" {
			aside := dst + ".old"
			os.Remove(aside)
			if renameErr := os.Rename(dst, aside); renameErr == nil {
				if err2 := os.Rename(staged, dst); err2 == nil {
					return nil
				}
				os.Rename(aside, dst)
			}
		}
		os.Remove(staged)
		return fmt.Errorf("could not install to %s: %w", dst, err)
	}
	return nil
}

// copyToTemp copies src to a new file beside dst and returns its path. The
// caller renames it over dst; on any error nothing outside the temp file has
// been touched.
func copyToTemp(src, dst string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	out, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return "", err
	}
	staged := out.Name()
	cleanup := func(err error) (string, error) {
		out.Close()
		os.Remove(staged)
		return "", err
	}

	if _, err := io.Copy(out, in); err != nil {
		return cleanup(err)
	}
	// CreateTemp makes the file 0600; the installed binary has to be runnable.
	if err := out.Chmod(0755); err != nil {
		return cleanup(err)
	}
	if err := out.Close(); err != nil {
		os.Remove(staged)
		return "", err
	}
	return staged, nil
}

// getInstallDir picks the directory to install into: a system-wide bin when we
// can write to it, otherwise a per-user one that never needs elevation.
func getInstallDir() string {
	switch runtime.GOOS {
	case "linux", "darwin":
		if isWritable("/usr/local/bin") {
			return "/usr/local/bin"
		}
		// Without a home directory the fallback would be the relative path
		// ".local/bin", installing into whatever the cwd happens to be.
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		return filepath.Join(home, ".local", "bin")
	case "windows":
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		// Per-user directory; created by the caller if missing, no elevation needed.
		return filepath.Join(home, "bin")
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
		if samePathEntry(entry, dir) {
			return true
		}
		// Tolerate a trailing separator and relative spellings of the same dir.
		if abs, err := filepath.Abs(entry); err == nil && samePathEntry(abs, filepath.Clean(dir)) {
			return true
		}
	}
	return false
}

// samePathEntry compares two PATH entries the way the platform does: Windows
// paths are case-insensitive, so a differently-cased entry is still a match.
func samePathEntry(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
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
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", "", errors.New("no home directory")
	}
	shell := os.Getenv("SHELL")
	// Keep the profile portable when the directory lives under $HOME.
	quoted := dir
	if strings.HasPrefix(dir, home+string(os.PathSeparator)) {
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
		return filepath.Join(home, bashProfile(home)),
			`export PATH="` + quoted + `:$PATH"`, nil
	}
	return "", "", errors.New("unrecognised shell")
}

// bashProfile names the file to add a PATH line to, relative to home.
//
// On Linux a terminal starts a non-login shell, which reads .bashrc. On macOS
// Terminal and iTerm start login shells, which never read .bashrc — they read
// the FIRST of .bash_profile, .bash_login and .profile that exists. Creating
// .bash_profile when the user keeps their setup in .profile would stop that
// file being read at all, so an existing one always wins.
func bashProfile(home string) string {
	if runtime.GOOS != "darwin" {
		return ".bashrc"
	}
	for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
		if _, err := os.Stat(filepath.Join(home, name)); err == nil {
			return name
		}
	}
	return ".bash_profile"
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
