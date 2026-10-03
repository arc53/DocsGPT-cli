package display

import (
	"os"
	"path/filepath"
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

// showBanner reports whether a chat shows the banner, per setting:
// "always", "once" (the default: the first chat only) or "never".
func showBanner(setting string) bool {
	switch setting {
	case "never":
		return false
	case "always":
		return true
	}
	if bannerShown() {
		return false
	}
	markBannerShown()
	return true
}

// bannerLines is the banner in width columns: the wordmark needs 80, so
// narrower terminals get the dino alone.
func bannerLines(width int) []string {
	var lines []string
	for _, line := range dinoArt {
		lines = append(lines, T.Accent.Render(line))
	}
	if width >= 80 {
		lines = append(lines, "")
		for _, line := range wordmark {
			lines = append(lines, T.Accent.Bold(true).Render(line))
		}
		lines = append(lines, T.Muted.Render(tagline))
	}
	return lines
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
