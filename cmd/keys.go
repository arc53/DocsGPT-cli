package cmd

import (
    "fmt"
    "strings"

    "github.com/spf13/cobra"
    "github.com/fatih/color"
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
    Run: func(cmd *cobra.Command, args []string) {
        keys := loadKeys()

        // Handle add flag
        if addKeyFlag {
            addKey(keys)
            saveKeys(keys)
            return
        }

        // Handle delete flag
        if deleteKeyFlag != "" {
            if _, exists := keys[deleteKeyFlag]; exists {
                deleteKeyByName(keys, deleteKeyFlag)
                saveKeys(keys)
            } else {
                printError("Key not found: " + deleteKeyFlag)
            }
            return
        }

        // Handle set flag
        if setKeyFlag != "" {
            if _, exists := keys[setKeyFlag]; exists {
                setNewDefaultKeyByName(keys, setKeyFlag)
                saveKeys(keys)
            } else {
                printError("Key not found: " + setKeyFlag)
            }
            return
        }

        // If no flags are provided, show available keys and prompt user for action
        fmt.Println("Available keys:")
        for name, key := range keys {
            if key.Default {
                green := color.New(color.FgGreen).SprintFunc()
                fmt.Printf(" - %s %s\n", name, green("(default)"))
            } else {
                fmt.Printf(" - %s\n", name)
            }
        }

        var action string
        fmt.Print("What would you like to do? (add/set/delete): ")
        fmt.Scanln(&action)

        switch strings.ToLower(action) {
        case "add":
            addKey(keys)
        case "set":
            setNewDefaultKey(keys)
        case "delete":
            deleteKey(keys)
        default:
            printError("Invalid action. Please choose add, set, or delete.")
        }

        saveKeys(keys)
    },
}

func init() {
    keysCmd.Flags().BoolVar(&addKeyFlag, "add", false, "Add a new API key")
    keysCmd.Flags().StringVar(&deleteKeyFlag, "delete", "", "Delete an API key by name")
    keysCmd.Flags().StringVar(&setKeyFlag, "set", "", "Set an API key as default by name")
}

func addKey(keys map[string]APIKey) {
    var name, apiKey string
    fmt.Print("Enter a name for this API key: ")
    fmt.Scanln(&name)
    fmt.Print("Please enter your DocsGPT API key: ")
    fmt.Scanln(&apiKey)

    // Automatically set the new key as the default
    setDefaultKey(keys, name)

    keys[name] = APIKey{Key: apiKey, Default: true}
    green := color.New(color.FgGreen).SprintFunc()
    fmt.Println(green("API key added and set as default successfully."))
}

func setNewDefaultKey(keys map[string]APIKey) {
    if len(keys) == 0 {
        printError("No keys available to set as default. Please add a key first.")
        return
    }

    var name string
    fmt.Print("Enter the name of the key to set as default: ")
    fmt.Scanln(&name)

    if _, exists := keys[name]; !exists {
        printError("Key not found. Please choose a valid key.")
        return
    }

    setDefaultKey(keys, name)
    green := color.New(color.FgGreen).SprintFunc()
    fmt.Println(green("Default key set successfully to:"), name)
}

func deleteKey(keys map[string]APIKey) {
    if len(keys) == 0 {
        printError("No keys available to delete.")
        return
    }

    var name string
    fmt.Print("Enter the name of the key to delete: ")
    fmt.Scanln(&name)

    deleteKeyByName(keys, name)
}

func deleteKeyByName(keys map[string]APIKey, name string) {
    if _, exists := keys[name]; !exists {
        printError("Key not found. Please choose a valid key.")
        return
    }

    delete(keys, name)
    green := color.New(color.FgGreen).SprintFunc()
    fmt.Println(green("API key deleted successfully."))

    // If the deleted key was the default, unset the default
    for _, key := range keys {
        if key.Default {
            return
        }
    }

    if len(keys) > 0 {
        // Set the first key as default
        for name := range keys {
            setDefaultKey(keys, name)
            fmt.Println("The first key was automatically set as the new default.")
            break
        }
    }
}

func setNewDefaultKeyByName(keys map[string]APIKey, name string) {
    if _, exists := keys[name]; !exists {
        printError("Key not found. Please choose a valid key.")
        return
    }

    setDefaultKey(keys, name)
    green := color.New(color.FgGreen).SprintFunc()
    fmt.Println(green("Default key set successfully to:"), name)
}
