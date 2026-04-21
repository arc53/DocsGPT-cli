package display

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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

// ShowBanner displays the animated startup banner.
// setting: "always", "once", "never" (empty defaults to "once").
func ShowBanner(setting string, noMotion bool) {
	if setting == "" {
		setting = "always"
	}
	if setting == "never" {
		return
	}

	// Skip for non-TTY, screen readers, dumb terminals
	if !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		return
	}
	if os.Getenv("TERM") == "dumb" {
		return
	}

	if setting == "once" && bannerShown() {
		return
	}

	accent := T.Accent
	accentBold := T.Accent.Bold(true)
	muted := T.Muted

	// No-motion: print everything at once
	if noMotion || os.Getenv("NO_MOTION") != "" {
		for _, line := range dinoArt {
			fmt.Println(accent.Render(line))
		}
		fmt.Println()
		for _, line := range wordmark {
			fmt.Println(accentBold.Render(line))
		}
		fmt.Println(muted.Render(tagline))
		fmt.Println()
		if setting == "once" {
			markBannerShown()
		}
		return
	}

	// === Animated version ===
	fmt.Print("\033[?25l") // hide cursor
	defer fmt.Print("\033[?25h")

	// Phase 1: Dino reveals line by line
	for _, line := range dinoArt {
		fmt.Println(accent.Render(line))
		time.Sleep(22 * time.Millisecond)
	}

	time.Sleep(40 * time.Millisecond)

	// Phase 2: Wordmark
	fmt.Println()
	for _, line := range wordmark {
		fmt.Println(accentBold.Render(line))
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	fmt.Println(muted.Render(tagline))

	time.Sleep(150 * time.Millisecond)
	fmt.Println()

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
