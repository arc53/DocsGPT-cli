package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"docsgpt-cli/internal/display"
	"docsgpt-cli/internal/update"

	"github.com/spf13/cobra"
)

var (
	updateCheckOnly bool
	updateYes       bool
	updateRollback  bool
	updateWorker    bool
)

var updateCmd = &cobra.Command{
	Use:          "update",
	Short:        "Update docsgpt-cli to the latest release",
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if updateWorker {
			update.RunWorker(Version)
			return nil
		}

		if !update.IsReleaseVersion(Version) {
			fmt.Println(display.Warn(fmt.Sprintf("This binary reports version %q, which is not a tagged release build.", Version)))
			fmt.Println("If you built from source, update with 'git pull && make build'.")
			return nil
		}

		if updateRollback {
			return runRollback()
		}

		fmt.Println(display.Muted("Checking for updates..."))
		rel, err := update.FetchLatest(10 * time.Second)
		if err != nil {
			return fmt.Errorf("could not check for updates: %w", err)
		}
		update.RecordCheck(rel)

		if !update.IsNewer(rel.TagName, Version) {
			fmt.Println(display.Success("Already up to date:"), Version)
			return nil
		}

		fmt.Printf("%s %s → %s\n", display.Accent("Update available:"), Version, rel.TagName)
		fmt.Println(display.Muted(rel.HTMLURL))

		if updateCheckOnly {
			if update.StagedVersion() == rel.TagName {
				fmt.Println("Already downloaded — it will be installed on your next command.")
			} else {
				fmt.Println("Run 'docsgpt-cli update' to install it.")
			}
			return nil
		}

		exePath, err := resolveExecutable()
		if err != nil {
			return err
		}
		if update.IsHomebrewPath(exePath) {
			fmt.Println(display.Warn("This binary is managed by Homebrew. Run 'brew upgrade docsgpt-cli' instead."))
			return nil
		}
		if !isWritable(filepath.Dir(exePath)) {
			return fmt.Errorf("no write permission for %s, re-run with sudo", filepath.Dir(exePath))
		}

		if !updateYes {
			fmt.Print("Proceed? [Y/n] ")
			input, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil {
				return err
			}
			input = strings.TrimSpace(strings.ToLower(input))
			if input != "" && input != "y" && input != "yes" {
				fmt.Println(display.Muted("Update cancelled."))
				return nil
			}
		}

		fmt.Println(display.Muted("Downloading " + update.AssetName() + "..."))
		if err := update.Apply(rel, exePath, Version); err != nil {
			return err
		}
		// A manual update overrides any rollback skip.
		update.SetSkipVersion("")
		fmt.Println(display.Success("Updated to " + rel.TagName))
		fmt.Println(display.Muted("Roll back anytime with 'docsgpt-cli update --rollback'."))
		return nil
	},
}

func runRollback() error {
	exePath, err := resolveExecutable()
	if err != nil {
		return err
	}
	if update.IsHomebrewPath(exePath) {
		fmt.Println(display.Warn("This binary is managed by Homebrew. Roll back with brew instead."))
		return nil
	}
	if !isWritable(filepath.Dir(exePath)) {
		return fmt.Errorf("no write permission for %s, re-run with sudo", filepath.Dir(exePath))
	}

	rolledTo, err := update.Rollback(Version, exePath)
	if err != nil {
		return err
	}
	if rolledTo == "" {
		rolledTo = "the previous version"
	}
	fmt.Println(display.Success("Rolled back to " + rolledTo))
	fmt.Println(display.Muted("Auto-update will skip " + Version + ". Run 'docsgpt-cli update' to reinstall it."))
	return nil
}

func resolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not determine the executable path: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("could not resolve the executable path: %w", err)
	}
	return realPath, nil
}

func init() {
	updateCmd.Flags().BoolVar(&updateCheckOnly, "check", false, "Only check for a new version, don't install")
	updateCmd.Flags().BoolVar(&updateYes, "yes", false, "Skip the confirmation prompt")
	updateCmd.Flags().BoolVar(&updateRollback, "rollback", false, "Restore the binary from before the last update")
	updateCmd.Flags().BoolVar(&updateWorker, "worker", false, "Run the background check/stage pass")
	updateCmd.Flags().MarkHidden("worker")
}
