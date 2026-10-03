package ui

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"

	"github.com/atotto/clipboard"
)

// maxOSC52 caps the base64 text of an OSC 52 sequence: terminals drop
// longer ones (pi's limit).
const maxOSC52 = 100_000

// Copy puts text on the clipboard: the system's, or the terminal's through
// an OSC 52 sequence written to term over SSH (where the system clipboard
// is the server's) and when the system clipboard fails (no xclip, say).
func Copy(text string, term io.Writer) error {
	if !remote() && systemClipboard(text) == nil {
		return nil
	}
	seq, err := osc52(text)
	if err != nil {
		return err
	}
	_, err = io.WriteString(term, seq)
	return err
}

var systemClipboard = clipboard.WriteAll

func remote() bool {
	return os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_CLIENT") != ""
}

// osc52 returns the sequence that sets the terminal's clipboard to text.
func osc52(text string) (string, error) {
	enc := base64.StdEncoding.EncodeToString([]byte(text))
	if len(enc) > maxOSC52 {
		return "", fmt.Errorf("too long for the terminal's clipboard (%d KB, at most %d KB)", (len(enc)+999)/1000, maxOSC52/1000)
	}
	return "\x1b]52;c;" + enc + "\a", nil
}
