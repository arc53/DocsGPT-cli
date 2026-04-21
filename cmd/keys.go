package cmd

import (
	"fmt"
	"strings"

	"docsgpt-cli/internal/config"
	"docsgpt-cli/internal/display"

	"github.com/spf13/cobra"
)

var (
	addKeyFlag    bool
	deleteKeyFlag string
	setKeyFlag    string
)

var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Manage DocsGPT API keys (add, set default, delete)",
	Long: `The keys command allows you to manage your DocsGPT API keys.

You can add a new API key, set an existing key as the default, or delete a key.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		// Handle add flag
		if addKeyFlag {
			addKey(&cfg)
			return cfg.Save()
		}

		// Handle delete flag
		if deleteKeyFlag != "" {
			if _, exists := cfg.Keys[deleteKeyFlag]; !exists {
				return fmt.Errorf("key not found: %s", deleteKeyFlag)
			}
			deleteKeyByName(&cfg, deleteKeyFlag)
			return cfg.Save()
		}

		// Handle set flag
		if setKeyFlag != "" {
			if _, exists := cfg.Keys[setKeyFlag]; !exists {
				return fmt.Errorf("key not found: %s", setKeyFlag)
			}
			cfg.DefaultKey = setKeyFlag
			fmt.Println(display.Success("Default key set successfully to:"), setKeyFlag)
			return cfg.Save()
		}

		// If no flags are provided, show available keys and prompt user for action
		fmt.Println("Available keys:")
		for name := range cfg.Keys {
			if name == cfg.DefaultKey {
				fmt.Printf(" - %s %s\n", name, display.Accent("(default)"))
			} else {
				fmt.Printf(" - %s\n", name)
			}
		}

		var action string
		fmt.Print("What would you like to do? (add/set/delete): ")
		fmt.Scanln(&action)

		switch strings.ToLower(action) {
		case "add":
			addKey(&cfg)
		case "set":
			setNewDefaultKey(&cfg)
		case "delete":
			deleteKeyInteractive(&cfg)
		default:
			return fmt.Errorf("invalid action. Please choose add, set, or delete")
		}

		return cfg.Save()
	},
}

func init() {
	keysCmd.Flags().BoolVar(&addKeyFlag, "add", false, "Add a new API key")
	keysCmd.Flags().StringVar(&deleteKeyFlag, "delete", "", "Delete an API key by name")
	keysCmd.Flags().StringVar(&setKeyFlag, "set", "", "Set an API key as default by name")
}

func addKey(cfg *config.Config) {
	var name, apiKey string
	fmt.Print("Enter a name for this API key: ")
	fmt.Scanln(&name)
	fmt.Print("Please enter your DocsGPT API key: ")
	fmt.Scanln(&apiKey)

	cfg.Keys[name] = apiKey
	cfg.DefaultKey = name

	fmt.Println(display.Success("API key added and set as default successfully."))
}

func setNewDefaultKey(cfg *config.Config) {
	if len(cfg.Keys) == 0 {
		printError("No keys available to set as default. Please add a key first.")
		return
	}

	var name string
	fmt.Print("Enter the name of the key to set as default: ")
	fmt.Scanln(&name)

	if _, exists := cfg.Keys[name]; !exists {
		printError("Key not found. Please choose a valid key.")
		return
	}

	cfg.DefaultKey = name
	fmt.Println(display.Success("Default key set successfully to:"), name)
}

func deleteKeyInteractive(cfg *config.Config) {
	if len(cfg.Keys) == 0 {
		printError("No keys available to delete.")
		return
	}

	var name string
	fmt.Print("Enter the name of the key to delete: ")
	fmt.Scanln(&name)

	deleteKeyByName(cfg, name)
}

func deleteKeyByName(cfg *config.Config, name string) {
	if _, exists := cfg.Keys[name]; !exists {
		printError("Key not found. Please choose a valid key.")
		return
	}

	delete(cfg.Keys, name)
	fmt.Println(display.Success("API key deleted successfully."))

	// If the deleted key was the default, pick a new one
	if cfg.DefaultKey == name {
		cfg.DefaultKey = ""
		for k := range cfg.Keys {
			cfg.DefaultKey = k
			fmt.Println("The first key was automatically set as the new default.")
			break
		}
	}
}
