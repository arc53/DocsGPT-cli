package cmd

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"

    "github.com/spf13/cobra"
    "github.com/fatih/color"
)

var installCmd = &cobra.Command{
    Use:   "install",
    Short: "Install docsgpt-cli to your system PATH",
    Run: func(cmd *cobra.Command, args []string) {
        binaryName := "docsgpt-cli"
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
            printError("Failed to move the binary to the installation path: " + err.Error())
            return
        }

        // For Windows, update the PATH using setx command
        if runtime.GOOS == "windows" {
            if err := addToWindowsPATH(filepath.Dir(destinationPath)); err != nil {
                printError("Failed to add to PATH: " + err.Error())
                return
            }
        }

        green := color.New(color.FgGreen).SprintFunc()
        fmt.Println(green("docsgpt-cli successfully installed! You can now use it with 'docsgpt-cli' command."))
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
        installDir = filepath.Join(os.Getenv("USERPROFILE"), "bin") // Use user bin directory
        if !isWritable(installDir) {
            installDir = filepath.Join("C:\\Windows\\System32")
        }
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
    // Update PATH using setx command on Windows
    cmd := exec.Command("setx", "PATH", fmt.Sprintf("%%PATH%%;%s", dir))
    return cmd.Run()
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
