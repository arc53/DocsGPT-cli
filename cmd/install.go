package cmd

import (
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

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install docsgpt-cli to your system PATH",
	Run: func(cmd *cobra.Command, args []string) {
		binaryName := "docsgpt-cli"
		if runtime.GOOS == "windows" {
			binaryName += ".exe"
		}
		sourcePath, err := os.Executable()
		if err != nil {
			printError("Failed to determine the executable path: " + err.Error())
			return
		}

		destinationPath := getInstallPath(binaryName)
		if destinationPath == "" {
			printError("Could not determine a suitable installation path for your OS.")
			return
		}

		// Ensure the target directory exists
		installDir := filepath.Dir(destinationPath)
		if _, err := os.Stat(installDir); os.IsNotExist(err) {
			err = os.MkdirAll(installDir, os.ModePerm)
			if err != nil {
				printError("Failed to create the installation directory: " + err.Error())
				return
			}
		}

		if err := os.Rename(sourcePath, destinationPath); err != nil {
			// Rename fails across volumes/drives; fall back to copying.
			if copyErr := copyFile(sourcePath, destinationPath); copyErr != nil {
				printError("Failed to move the binary to the installation path: " + copyErr.Error())
				return
			}
		}

		if runtime.GOOS == "windows" {
			if err := addToWindowsPATH(filepath.Dir(destinationPath)); err != nil {
				printError("Failed to add to PATH: " + err.Error())
				return
			}
			fmt.Println(display.Success("docsgpt-cli successfully installed! Open a new terminal to pick up the PATH change, then use the 'docsgpt-cli' command."))
			return
		}

		fmt.Println(display.Success("docsgpt-cli successfully installed! You can now use it with 'docsgpt-cli' command."))
	},
}

func getInstallPath(binaryName string) string {
	var installDir string

	switch runtime.GOOS {
	case "linux", "darwin":
		installDir = "/usr/local/bin/" // Typical path for Unix-like systems
		if !isWritable(installDir) {
			installDir = filepath.Join(os.Getenv("HOME"), ".local/bin/")
		}
	case "windows":
		// Per-user directory; created below if missing, no elevation needed.
		installDir = filepath.Join(os.Getenv("USERPROFILE"), "bin")
	default:
		return ""
	}

	return filepath.Join(installDir, binaryName)
}

func isWritable(dir string) bool {
	testFile := filepath.Join(dir, ".testwrite")
	if err := os.WriteFile(testFile, []byte{}, 0644); err != nil {
		return false
	}
	os.Remove(testFile)
	return true
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

func addToPATH(binaryPath string) error {
	shellConfigPath, shellConfigFound := getShellConfigPath()

	if !shellConfigFound {
		return fmt.Errorf("unable to find shell configuration file")
	}

	pathEntry := fmt.Sprintf("export PATH=\"$HOME/.local/bin:$PATH\"")
	return appendToShellConfig(shellConfigPath, pathEntry)
}

func getShellConfigPath() (string, bool) {
	homeDir := os.Getenv("HOME")
	shell := os.Getenv("SHELL")

	if strings.Contains(shell, "zsh") {
		return filepath.Join(homeDir, ".zshrc"), true
	} else if strings.Contains(shell, "bash") {
		return filepath.Join(homeDir, ".bashrc"), true
	} else if strings.Contains(shell, "fish") {
		return filepath.Join(homeDir, ".config/fish/config.fish"), true
	}

	return "", false
}

func appendToShellConfig(configPath, content string) error {
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("unable to open shell config: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(content + "\n"); err != nil {
		return fmt.Errorf("unable to write to shell config: %w", err)
	}

	return nil
}
