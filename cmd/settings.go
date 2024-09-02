package cmd

import (
    "fmt"
    "strconv"
    "github.com/manifoldco/promptui"
    "github.com/spf13/cobra"
)

var (
    toggleCurrentDirFlag   bool
    toggleDirContentsFlag  bool
    toggleLastCommandsFlag bool
    setNumLastCommandsFlag int
)

var settingsCmd = &cobra.Command{
    Use:   "settings",
    Short: "Configure the settings for docsgpt-cli",
    Long: `The settings command allows you to configure various options for docsgpt-cli.

You can toggle whether to send the current directory, directory contents, and last commands, as well as specify the number of last commands to send (up to 10).`,
    Run: func(cmd *cobra.Command, args []string) {
        settings := loadSettings()

        if toggleCurrentDirFlag {
            settings.SendCurrentDirectory = !settings.SendCurrentDirectory
        }

        if toggleDirContentsFlag {
            settings.SendDirectoryContents = !settings.SendDirectoryContents
        }

        if toggleLastCommandsFlag {
            settings.SendLastCommands = !settings.SendLastCommands
        }

        if setNumLastCommandsFlag > 0 {
            settings.NumberOfLastCommands = setNumLastCommandsFlag
        }

        saveSettings(settings)

        if toggleCurrentDirFlag || toggleDirContentsFlag || toggleLastCommandsFlag || setNumLastCommandsFlag > 0 {
            fmt.Println("Settings updated.")
            return
        }

        // If no flags were used, show the interactive menu
        options := []string{
            fmt.Sprintf("Send Current Directory: %t", settings.SendCurrentDirectory),
            fmt.Sprintf("Send Directory Contents: %t", settings.SendDirectoryContents),
            fmt.Sprintf("Send Last Commands: %t", settings.SendLastCommands),
            fmt.Sprintf("Number of Last Commands to Send: %d", settings.NumberOfLastCommands),
            "Save and Exit",
        }

        for {
            prompt := promptui.Select{
                Label: "Settings",
                Items: options,
            }

            _, result, err := prompt.Run()
            if err != nil {
                fmt.Println("Prompt failed:", err)
                return
            }

            switch result {
            case options[0]: // Toggle SendCurrentDirectory
                settings.SendCurrentDirectory = !settings.SendCurrentDirectory
            case options[1]: // Toggle SendDirectoryContents
                settings.SendDirectoryContents = !settings.SendDirectoryContents
            case options[2]: // Toggle SendLastCommands
                settings.SendLastCommands = !settings.SendLastCommands
            case options[3]: // Set NumberOfLastCommands
                numberPrompt := promptui.Prompt{
                    Label:    "Enter number of last commands to send (1-10)",
                    Validate: validateNumberOfCommands,
                }
                numberStr, err := numberPrompt.Run()
                if err == nil {
                    num, _ := strconv.Atoi(numberStr)
                    settings.NumberOfLastCommands = num
                }
            case options[4]: // Save and Exit
                saveSettings(settings)
                fmt.Println("Settings saved.")
                return
            }

            // Update the options to reflect the new settings
            options = []string{
                fmt.Sprintf("Send Current Directory: %t", settings.SendCurrentDirectory),
                fmt.Sprintf("Send Directory Contents: %t", settings.SendDirectoryContents),
                fmt.Sprintf("Send Last Commands: %t", settings.SendLastCommands),
                fmt.Sprintf("Number of Last Commands to Send: %d", settings.NumberOfLastCommands),
                "Save and Exit",
            }
        }
    },
}

func validateNumberOfCommands(input string) error {
    num, err := strconv.Atoi(input)
    if err != nil || num < 1 || num > 10 {
        return fmt.Errorf("invalid number")
    }
    return nil
}

func init() {
    settingsCmd.Flags().BoolVar(&toggleCurrentDirFlag, "toggle-dir", false, "Toggle sending current directory")
    settingsCmd.Flags().BoolVar(&toggleDirContentsFlag, "toggle-contents", false, "Toggle sending directory contents")
    settingsCmd.Flags().BoolVar(&toggleLastCommandsFlag, "toggle-commands", false, "Toggle sending last commands")
    settingsCmd.Flags().IntVar(&setNumLastCommandsFlag, "set-num-commands", 0, "Set number of last commands to send (1-10)")

    rootCmd.AddCommand(settingsCmd)
}
