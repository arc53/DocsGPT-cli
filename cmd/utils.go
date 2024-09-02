package cmd

import (
    "encoding/json"
    "fmt"
    "io/ioutil"
    "os"
    "path/filepath"

    "github.com/fatih/color"
)

type APIKey struct {
    Key     string `json:"key"`
    Default bool   `json:"default"`
}

type Settings struct {
    SendCurrentDirectory bool `json:"send_current_directory"`
    SendDirectoryContents bool `json:"send_directory_contents"`
    SendLastCommands      bool `json:"send_last_commands"`
    NumberOfLastCommands  int  `json:"number_of_last_commands"`
}

var (
    keysFile     string
    settingsFile string
)

func init() {
    homeDir, _ := os.UserHomeDir()
    keysFile = filepath.Join(homeDir, ".docsgpt-keys.json")     // JSON file to store multiple keys
    settingsFile = filepath.Join(homeDir, ".docsgpt-settings.json") // JSON file to store settings
}

func printError(message string) {
    red := color.New(color.FgRed).SprintFunc()
    fmt.Printf("%s %s\n", red("Error:"), message)
}

func loadKeys() map[string]APIKey {
    keys := make(map[string]APIKey)
    data, err := ioutil.ReadFile(keysFile)
    if err == nil && len(data) > 0 {
        json.Unmarshal(data, &keys)
    }
    return keys
}

func saveKeys(keys map[string]APIKey) {
    data, err := json.MarshalIndent(keys, "", "  ")
    if err != nil {
        printError("Failed to save API keys: " + err.Error())
        return
    }
    err = ioutil.WriteFile(keysFile, data, 0600)
    if err != nil {
        printError("Failed to write API keys file: " + err.Error())
    }
}

func getDefaultKey(keys map[string]APIKey) (string, APIKey) {
    for name, key := range keys {
        if key.Default {
            return name, key
        }
    }
    printError("No default key set. Use 'keys' to set one.")
    return "", APIKey{}
}

func setDefaultKey(keys map[string]APIKey, defaultName string) {
    for name := range keys {
        keys[name] = APIKey{Key: keys[name].Key, Default: name == defaultName}
    }
}

func loadSettings() Settings {
    var settings Settings
    data, err := ioutil.ReadFile(settingsFile)
    if err == nil && len(data) > 0 {
        json.Unmarshal(data, &settings)
    } else {
        // Default settings
        settings = Settings{
            SendCurrentDirectory: true,
            SendDirectoryContents: true,
            SendLastCommands: true,
            NumberOfLastCommands: 3,
        }
    }
    return settings
}

func saveSettings(settings Settings) {
    data, err := json.MarshalIndent(settings, "", "  ")
    if err != nil {
        printError("Failed to save settings: " + err.Error())
        return
    }
    err = ioutil.WriteFile(settingsFile, data, 0600)
    if err != nil {
        printError("Failed to write settings file: " + err.Error())
    }
}
