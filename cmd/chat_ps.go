package cmd

import (
	"fmt"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/tools"
	"github.com/arc53/DocsGPT-cli/internal/ui"
)

// processList shows the background processes and monitors of the chat
// (/ps): pick one to see its output or stop it.
func (s *chatSession) processList(string) {
	at := 0
	for {
		list := s.procs.List()
		if len(list) == 0 {
			s.note("No background processes in this chat. The agent starts them with run_command (run_in_background) and local_monitor.")
			return
		}
		items := make([]ui.Item, len(list))
		for i, p := range list {
			what := "$ " + p.Command
			if p.Monitor {
				what = fmt.Sprintf("monitor %q", p.Description)
			}
			items[i] = ui.Item{Label: display.Safe(p.ID + " · " + oneLine(what, 70)), Value: p.ID, Description: p.Status()}
		}
		id, err := s.scr.Select(ui.Select{Title: "Background processes", Items: items, Default: min(at, len(items)-1), Filter: len(items) > 8})
		if err != nil {
			return
		}
		for i, p := range list {
			if p.ID == id {
				at = i
			}
		}
		if !s.processAction(list[at]) {
			return
		}
	}
}

// processAction asks what to do with process p: show its output, stop it.
// It reports whether to show the list again.
func (s *chatSession) processAction(p tools.Info) bool {
	items := []ui.Item{{Label: "Show output", Value: "output", Keys: []string{"o"}, Description: "the last lines of its output, in the transcript"}}
	if p.Running {
		items = append(items, ui.Item{Label: "Stop", Value: "stop", Keys: []string{"s"}, Description: "stop it with everything it started; the agent is told"})
	}
	items = append(items, ui.Item{Label: "Back", Value: "back", Keys: []string{"b"}, Description: "the list again"})
	v, err := s.scr.Select(ui.Select{Title: display.Safe(p.ID + " · " + oneLine(p.Command, 70)), Items: items, Inline: true})
	if err != nil {
		return false
	}
	switch v {
	case "output":
		_, text, err := s.procs.Output(p.ID, 200)
		if err != nil {
			s.fail(err.Error())
			return false
		}
		block := display.NewToolBlock(display.Safe("output "+p.ID), display.Safe(oneLine(p.Command, 80)))
		block.Write([]byte(text))
		block.Close(true, p.Status()+" · "+display.ShortPath(p.Log))
		s.scr.Add(block)
		return false
	case "stop":
		info, err := s.procs.Stop(p.ID, true)
		if err != nil {
			s.fail(err.Error())
			return false
		}
		s.scr.Add(display.Done("Stopped " + info.ID + "; the agent is told with your next message."))
		s.footer()
		return false
	}
	return true
}

// oneLine shows text on one line, cut at n runes.
func oneLine(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}
