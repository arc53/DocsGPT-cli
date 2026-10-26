package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/display"
)

// backgrounder is a UI that lets the user move a running command to the
// background (the chat's Ctrl+B): move is called then, until the returned
// func withdraws it.
type backgrounder interface {
	Backgroundable(move func()) (withdraw func())
}

// guard refuses a command that would detach (see detaches) or, unless it
// goes to the background, sleep for long (longSleep), telling the model
// what to do instead. It returns "" when the command may be asked about.
func (s *Session) guard(command string, background bool) string {
	if s.Processes == nil {
		return ""
	}
	if how := detaches(command); how != "" {
		return s.fail("$ "+command, "not run: "+how+" would detach it",
			fmt.Sprintf("Error: not run: %s would leave the command running on its own, beyond docsgpt-cli's reach (it would outlive the %s and its output would be lost). "+
				"Run it with run_in_background instead (without %s): it returns at once with an id, keeps the output, and you are told when it exits.", how, s.scope(), how))
	}
	if d := longSleep(command); d > 0 && !background {
		wait := "and end your turn: you are told when it exits"
		if s.Processes.Wake == nil {
			wait = "and go on: you are told when it exits with the results of your later tool calls"
		}
		return s.fail("$ "+command, "not run: sleeps "+timeoutText(d),
			fmt.Sprintf("Error: not run: sleeping %s blocks while nothing happens. To wait for something, run a command that ends when it is true with run_in_background "+
				"(until curl -sf localhost:3000; do sleep 1; done) %s; use local_monitor to be told of each matching line.", timeoutText(d), wait))
	}
	return ""
}

// scope is what background processes last as long as: the chat, or the
// answer of ask.
func (s *Session) scope() string {
	if s.Processes.Wake == nil {
		return "answer"
	}
	return "chat"
}

// startBackground runs command in the background, once approved; with a
// timeout it is stopped then.
func (s *Session) startBackground(cancel context.CancelFunc, command, dir string, timeout time.Duration) string {
	if strings.TrimSpace(command) == "" {
		return s.fail("$", "empty command", "Error: the command is empty.")
	}
	if refusal := s.guard(command, true); refusal != "" {
		return refusal
	}
	notes := []string{"in the background"}
	var dirReason string
	if dir != "" {
		notes = append(notes, "in "+display.ShortPath(dir))
		dirReason = readReason(resolve(dir))
	}
	if timeout > 0 {
		notes = append(notes, "timeout "+timeoutText(timeout))
	}
	if dirReason != "" {
		notes = append(notes, "("+dirReason+")")
	}
	command, edited, refusal := s.approveCommand(cancel, command, strings.Join(notes, " · "), dirReason)
	if refusal != "" {
		return refusal
	}
	info, err := s.Processes.Start(command, dir, timeout)
	if err != nil {
		s.ui().Close(false, err.Error())
		return "Error: " + err.Error()
	}
	s.ui().Close(true, "started "+info.ID)
	result := fmt.Sprintf("Started %s in the background on this computer: $ %s\nIts output goes to %s (local_output %s shows it).\n", info.ID, command, info.Log, info.ID)
	if edited {
		result = "[The user edited the command.]\n" + result
	}
	if s.Processes.Wake == nil {
		return result + "It is stopped when your answer ends. You are told when it exits with the results of your later tool calls."
	}
	return result + "You'll get a <local_process_event> when it exits. Until then don't sleep or poll: go on with other work, or end your turn when there is nothing else to do."
}

// startMonitor runs command as a monitor (see Processes.Monitor), once
// approved, for timeout (monitorDefault when 0, at most monitorMax).
func (s *Session) startMonitor(cancel context.CancelFunc, command, desc, dir string, timeout time.Duration) string {
	if strings.TrimSpace(command) == "" {
		return s.fail("$", "empty command", "Error: the command is empty.")
	}
	if refusal := s.guard(command, true); refusal != "" {
		return refusal
	}
	if timeout <= 0 {
		timeout = monitorDefault
	}
	timeout = min(timeout, monitorMax)
	if desc = oneLine(desc, 80); desc == "" {
		desc = oneLine(command, 40)
	}
	notes := []string{fmt.Sprintf("monitor %q · %s", desc, timeoutText(timeout))}
	var dirReason string
	if dir != "" {
		notes = append(notes, "in "+display.ShortPath(dir))
		dirReason = readReason(resolve(dir))
	}
	if dirReason != "" {
		notes = append(notes, "("+dirReason+")")
	}
	command, edited, refusal := s.approveCommand(cancel, command, display.Safe(strings.Join(notes, " · ")), dirReason)
	if refusal != "" {
		return refusal
	}
	info, err := s.Processes.Monitor(command, dir, desc, timeout)
	if err != nil {
		s.ui().Close(false, err.Error())
		return "Error: " + err.Error()
	}
	s.ui().Close(true, "started "+info.ID)
	result := fmt.Sprintf("Started monitor %s (%q) on this computer: $ %s\nIt stops after %s, when the command exits, or with local_stop %s.\n", info.ID, desc, command, timeoutText(timeout), info.ID)
	if edited {
		result = "[The user edited the command.]\n" + result
	}
	if s.Processes.Wake == nil {
		return result + "Each line it prints comes to you as a <local_process_event> with the results of your later tool calls; it is stopped when your answer ends."
	}
	return result + "Each line it prints comes to you as a <local_process_event>, in a new turn when you are idle: don't poll, go on or end your turn."
}

// processOutput shows process id (all of them when "") to the model.
func (s *Session) processOutput(id string, n int) string {
	if n <= 0 {
		n = 100
	}
	n = min(n, maxOutputLines)
	if strings.TrimSpace(id) == "" {
		list := s.Processes.List()
		s.ui().Open("local processes", "")
		if len(list) == 0 {
			s.ui().Close(true, "none")
			return "No background processes or monitors in this " + s.scope() + "."
		}
		var b strings.Builder
		for _, i := range list {
			b.WriteString(i.Describe() + "\n")
		}
		s.ui().Close(true, fmt.Sprintf("%d %s", len(list), plural(len(list), "process", "processes")))
		return strings.TrimRight(b.String(), "\n")
	}
	info, text, err := s.Processes.Output(id, n)
	if err != nil {
		return s.fail("output "+display.Safe(id), "no such process", "Error: "+err.Error())
	}
	s.ui().Open("output "+info.ID, display.Safe(oneLine(info.Command, 80)))
	shown := len(splitLines(text))
	s.ui().Close(true, info.Status()+" · "+lines(shown))
	if strings.TrimSpace(text) == "" {
		text = "(no output yet)"
	}
	return fmt.Sprintf("%s\nLog: %s\n%s", info.Describe(), info.Log, text)
}

// stopProcess stops process id for the model.
func (s *Session) stopProcess(id string) string {
	before, _, err := s.Processes.Output(id, 0)
	if err != nil {
		return s.fail("stop "+display.Safe(id), "no such process", "Error: "+err.Error())
	}
	s.ui().Open("stop "+before.ID, display.Safe(oneLine(before.Command, 80)))
	info, _ := s.Processes.Stop(id, false)
	if !before.Running {
		s.ui().Close(true, "had ended")
		return fmt.Sprintf("%s had already ended: %s", info.ID, info.Describe())
	}
	s.ui().Close(true, "stopped")
	return fmt.Sprintf("Stopped %s and everything it started.", info.ID)
}

// moved hands a command the user moved to the background (Ctrl+B) over to
// the Processes, and tells the model.
func (s *Session) moved(r *shellRun, command string, started time.Time, out *tailBuffer, edited bool) string {
	info, err := s.Processes.Adopt(r, command, started, func() string { return string(out.buf) })
	if err != nil {
		s.ui().Close(false, "stopped: "+err.Error())
		return "[The user tried to move the command to the background, but it was stopped: " + err.Error() + "]"
	}
	s.ui().Close(true, "moved to the background as "+info.ID)
	so := out.String()
	if strings.TrimSpace(so) == "" {
		so = "(no output yet)"
	}
	result := fmt.Sprintf("%s\n[The user moved this command to the background as %s: it keeps running on this computer, its output going to %s. "+
		"You'll get a <local_process_event> when it exits; don't wait for it. local_output %s shows its output.]", so, info.ID, info.Log, info.ID)
	if edited {
		result = fmt.Sprintf("[The user edited the command to: %s]\n%s", command, result)
	}
	return result
}
