package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/display"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

func printError(message string) {
	display.ErrorMsg(message)
}

// codeBlock is a fenced code block of an answer.
type codeBlock struct{ lang, code string }

var fenceRe = regexp.MustCompile("(?ms)^```([^\\s`]*)[^\\n]*\\n(.*?)^```")

// codeBlocks lists the fenced code blocks of markdown text.
func codeBlocks(md string) []codeBlock {
	var out []codeBlock
	for _, m := range fenceRe.FindAllStringSubmatch(md, -1) {
		out = append(out, codeBlock{m[1], strings.TrimSuffix(m[2], "\n")})
	}
	return out
}

// extractCommand returns the first bash or sh block of an answer.
func extractCommand(answer string) string {
	for _, b := range codeBlocks(answer) {
		if b.lang == "bash" || b.lang == "sh" {
			return b.code
		}
	}
	return ""
}

// copyToClipboard copies command and says so in a dim line.
func copyToClipboard(command string) {
	command = strings.TrimSpace(command)
	if err := clipboard.WriteAll(command); err != nil {
		printError("Failed to copy to clipboard: " + err.Error())
		return
	}
	first, rest, multi := strings.Cut(command, "\n")
	first = ansi.Truncate(first, 50, "…")
	if multi {
		n := strings.Count(rest, "\n") + 1
		first += fmt.Sprintf(" (+%d more %s)", n, plural(n, "line", "lines"))
	}
	fmt.Println(display.Success("✓") + " " + display.Dim("Copied to clipboard: "+first))
}
