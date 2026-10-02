package display

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
)

// Pixel-art T-rex silhouette inspired by the Chrome offline runner sprite.
// Facing right with the squared head, jaw notch, tiny arm, and heavy legs.
var dinoArt = []string{
	`                                    ▄███████████▄`,
	`                                 ███████████████████`,
	`                                 ████████████ ██████`,
	`                                 ███████████████████`,
	`                                 ██████████████████▀`,
	`                               ██████████████`,
	`                               ██████████████████`,
	`                           ███████████████`,
	`                       ██████████████████`,
	`                     ███████████████████████`,
	`                   ██████████████████████ ▄██`,
	`                  ██████████████████████`,
	`                ███████████████████████`,
	`              ████████████████████████`,
	`           ██████████████████████████`,
	`        ██████████████ ████████████`,
	`    ███████████████   ██████  █████`,
	`                    ██████    ████`,
	`                    █████     ██████`,
	`                    ███████   ████████`,
	`                    ▀▀▀▀▀▀▀   ▀▀▀▀▀▀▀▀`,
}

var wordmark = []string{
	`██████╗  ██████╗  ██████╗███████╗ ██████╗ ██████╗ ████████╗   ██████╗██╗     ██╗`,
	`██╔══██╗██╔═══██╗██╔════╝██╔════╝██╔════╝ ██╔══██╗╚══██╔══╝  ██╔════╝██║     ██║`,
	`██║  ██║██║   ██║██║     ███████╗██║  ███╗██████╔╝   ██║     ██║     ██║     ██║`,
	`██║  ██║██║   ██║██║     ╚════██║██║   ██║██╔═══╝    ██║     ██║     ██║     ██║`,
	`██████╔╝╚██████╔╝╚██████╗███████║╚██████╔╝██║        ██║     ╚██████╗███████╗██║`,
	`╚═════╝  ╚═════╝  ╚═════╝╚══════╝ ╚═════╝ ╚═╝        ╚═╝      ╚═════╝╚══════╝╚═╝`,
}

var tagline = `              ━━━━━━━  Terminal AI Assistant  ━━━━━━━`

// ShowBanner prints the startup banner of an interactive chat, all at once.
// setting: "always", "once", "never" (empty defaults to "once"). The
// wordmark needs 80 columns; narrower terminals get the dino alone.
func ShowBanner(setting string) {
	if setting == "" {
		setting = "once"
	}
	if setting == "never" || !isatty.IsTerminal(os.Stdout.Fd()) || os.Getenv("TERM") == "dumb" {
		return
	}
	if setting == "once" && bannerShown() {
		return
	}
	var b strings.Builder
	for _, line := range dinoArt {
		b.WriteString(T.Accent.Render(line) + "\n")
	}
	if termWidth() >= 80 {
		b.WriteString("\n")
		for _, line := range wordmark {
			b.WriteString(T.Accent.Bold(true).Render(line) + "\n")
		}
		b.WriteString(T.Muted.Render(tagline) + "\n")
	}
	fmt.Println(b.String())
	if setting == "once" {
		markBannerShown()
	}
}

func bannerShown() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	sentinel := filepath.Join(home, ".docsgpt", ".banner-shown")
	_, err = os.Stat(sentinel)
	return err == nil
}

func markBannerShown() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".docsgpt")
	os.MkdirAll(dir, 0700)
	sentinel := filepath.Join(dir, ".banner-shown")
	os.WriteFile(sentinel, []byte("1"), 0600)
}
