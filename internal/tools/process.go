package tools

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/arc53/DocsGPT-cli/internal/config"
	"github.com/arc53/DocsGPT-cli/internal/display"
)

// Limits of the background processes of a chat or ask run.
const (
	maxProcesses = 20 // running at once

	monitorDefault = 5 * time.Minute  // a monitor's timeout when the model gives none
	monitorMax     = 30 * time.Minute // and at most

	eventDelay   = 200 * time.Millisecond // lines this close together are one event
	eventBurst   = 10                     // a monitor's events before the rate applies
	eventEvery   = 2 * time.Second        // then one per
	eventLines   = 50                     // lines an event holds; more are counted
	eventBytes   = 3000                   // and bytes
	floodLines   = 300                    // lines in floodWindow that stop a monitor
	floodWindow  = 30 * time.Second
	exitTail     = 20 // lines of output an exit event shows
	logKeep      = 3 * 24 * time.Hour
	maxLineBytes = 4096 // a monitor's line in an event, cut beyond
)

// Processes runs the commands the model starts in the background
// (run_command with run_in_background, local_monitor, a command the user
// moves there with Ctrl+B) for one chat or ask run: each in a process group
// of its own, its output in a log file, all of them stopped by Close. What
// they do comes back as events: an exit, a monitor's lines.
type Processes struct {
	// Wake, when set, is called (from any goroutine) once events wait,
	// eventDelay after the first: the chat sends them to the model. Ask
	// leaves it nil; its events go with the next tool result only.
	Wake func()

	mu      sync.Mutex
	dir     string // the logs, made with the first process
	list    []*process
	next    int
	events  []Event
	waking  *time.Timer
	closed  bool
	started time.Time
}

// NewProcesses returns an empty set; wake is Processes.Wake.
func NewProcesses(wake func()) *Processes {
	return &Processes{Wake: wake, started: time.Now()}
}

// Event is something a background process did, for the model.
type Event struct {
	ID   string
	Text string // for the model and the transcript
}

// process is one background command or monitor.
type process struct {
	id, command, desc string
	monitor           bool
	started           time.Time
	logPath           string
	log               *os.File // O_APPEND: the command writes to it too
	run               *shellRun
	linesDone         chan struct{} // a monitor's stdout is read to its end

	// Once it ended:
	ended  bool
	at     time.Time
	exit   int    // -1 when it did not exit by itself
	how    string // "" (exited), "stopped", "stopped by the user", "expired", "flooded"
	quiet  bool   // no exit event: the model stopped it
	expiry *time.Timer

	// A monitor's events: the lines waiting, the ones dropped, the rate.
	batch   []string
	bytes   int
	dropped int
	flush   *time.Timer
	tokens  float64
	refill  time.Time
	sent    int // events sent
	window  []time.Time
	timeout time.Duration
}

// Info describes a process for /ps and the footer.
type Info struct {
	ID, Command, Description string
	Monitor, Running         bool
	Started, Ended           time.Time
	Exit                     int
	How                      string
	Log                      string
	Left                     time.Duration // a running monitor's time left
}

// logDir is where this run's logs go: one directory per run under
// ~/.docsgpt/logs/processes, the ones older than logKeep removed.
func (ps *Processes) logDir() (string, error) {
	if ps.dir != "" {
		return ps.dir, nil
	}
	root := filepath.Join(config.Dir(), "logs", "processes")
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if fi, err := e.Info(); err == nil && e.IsDir() && time.Since(fi.ModTime()) > logKeep {
				os.RemoveAll(filepath.Join(root, e.Name()))
			}
		}
	}
	dir := filepath.Join(root, fmt.Sprintf("%s-%d", ps.started.Format("20060102-150405"), os.Getpid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	ps.dir = dir
	return dir, nil
}

// add registers a new process, its log open; the caller starts it.
func (ps *Processes) add(command, desc string, monitor bool) (*process, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.closed {
		return nil, fmt.Errorf("the chat is ending")
	}
	running := 0
	for _, p := range ps.list {
		if !p.ended {
			running++
		}
	}
	if running >= maxProcesses {
		return nil, fmt.Errorf("%d background processes already run; stop one with local_stop first", running)
	}
	dir, err := ps.logDir()
	if err != nil {
		return nil, err
	}
	ps.next++
	p := &process{id: fmt.Sprintf("p%d", ps.next), command: command, desc: desc, monitor: monitor, started: time.Now(), tokens: eventBurst}
	p.logPath = filepath.Join(dir, p.id+".log")
	if p.log, err = os.OpenFile(p.logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0o600); err != nil {
		ps.next--
		return nil, err
	}
	ps.list = append(ps.list, p)
	return p, nil
}

// drop removes a process that did not start.
func (ps *Processes) drop(p *process) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p.log.Close()
	os.Remove(p.logPath)
	for i, q := range ps.list {
		if q == p {
			ps.list = append(ps.list[:i], ps.list[i+1:]...)
			break
		}
	}
}

// Start runs command in the background in dir, its output in its log; a
// timeout over 0 stops it then.
func (ps *Processes) Start(command, dir string, timeout time.Duration) (Info, error) {
	p, err := ps.add(command, "", false)
	if err != nil {
		return Info{}, err
	}
	if p.run, err = startShell(command, dir, p.log, nil); err != nil {
		ps.drop(p)
		return Info{}, err
	}
	if timeout > 0 {
		ps.mu.Lock()
		p.expiry = time.AfterFunc(timeout, func() { ps.end(p, "timed out") })
		ps.mu.Unlock()
	}
	go ps.wait(p)
	return ps.info(p), nil
}

// Monitor runs command in the background as a monitor: each line of its
// stdout is an event, its stderr goes only to its log. It is stopped after
// timeout.
func (ps *Processes) Monitor(command, dir, desc string, timeout time.Duration) (Info, error) {
	p, err := ps.add(command, desc, true)
	if err != nil {
		return Info{}, err
	}
	p.timeout = timeout
	pr, pw, err := os.Pipe()
	if err != nil {
		ps.drop(p)
		return Info{}, err
	}
	p.run, err = startShell(command, dir, pw, p.log)
	pw.Close()
	if err != nil {
		pr.Close()
		ps.drop(p)
		return Info{}, err
	}
	ps.mu.Lock()
	p.expiry = time.AfterFunc(timeout, func() { ps.end(p, "expired") })
	p.linesDone = make(chan struct{})
	ps.mu.Unlock()
	go ps.lines(p, pr)
	go ps.wait(p)
	return ps.info(p), nil
}

// Adopt takes over a command that ran in the foreground since started
// (moved with Ctrl+B): what it printed so far (before, read as its output
// is held) goes first into its log, then the rest.
func (ps *Processes) Adopt(r *shellRun, command string, started time.Time, before func() string) (Info, error) {
	p, err := ps.add(command, "", false)
	if err != nil {
		r.cancel()
		return Info{}, err
	}
	p.run, p.started = r, started
	r.out.handOver(p.log, before)
	go ps.wait(p)
	return ps.info(p), nil
}

// lines reads a monitor's stdout, a line at a time, into events.
func (ps *Processes) lines(p *process, r io.ReadCloser) {
	defer close(p.linesDone)
	defer r.Close()
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			p.log.WriteString(line)
			ps.line(p, strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			return
		}
	}
}

// line adds one line of a monitor's to its next event, unless it floods.
func (ps *Processes) line(p *process, line string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if p.ended || ps.closed {
		return
	}
	now := time.Now()
	p.window = append(p.window, now)
	for len(p.window) > 0 && now.Sub(p.window[0]) > floodWindow {
		p.window = p.window[1:]
	}
	if len(p.window) > floodLines {
		go ps.end(p, "flooded")
		return
	}
	if len(line) > maxLineBytes {
		line = strings.ToValidUTF8(line[:maxLineBytes], "") + " … [line cut]"
	}
	line = display.StripControls(line)
	if len(p.batch) >= eventLines || p.bytes+len(line) > eventBytes {
		p.dropped++
	} else {
		p.batch = append(p.batch, line)
		p.bytes += len(line) + 1
	}
	if p.flush == nil {
		p.flush = time.AfterFunc(ps.delay(p, now), func() { ps.flush(p) })
	}
}

// delay is how long a monitor's lines wait before they go: eventDelay, or
// until the rate allows another event.
func (ps *Processes) delay(p *process, now time.Time) time.Duration {
	if !p.refill.IsZero() {
		p.tokens = min(eventBurst, p.tokens+now.Sub(p.refill).Seconds()/eventEvery.Seconds())
	}
	p.refill = now
	if p.tokens >= 1 {
		return eventDelay
	}
	return max(eventDelay, time.Duration((1-p.tokens)*float64(eventEvery)))
}

// flush makes an event of a monitor's waiting lines.
func (ps *Processes) flush(p *process) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.flushLocked(p)
}

func (ps *Processes) flushLocked(p *process) {
	if p.flush != nil {
		p.flush.Stop()
		p.flush = nil
	}
	if len(p.batch) == 0 && p.dropped == 0 {
		return
	}
	now := time.Now()
	p.tokens = min(eventBurst, p.tokens+now.Sub(p.refill).Seconds()/eventEvery.Seconds()) - 1
	p.refill = now
	p.sent++
	var b strings.Builder
	fmt.Fprintf(&b, "%s · monitor %q: %s\n", p.id, p.desc, lines(len(p.batch)+p.dropped))
	for _, l := range p.batch {
		b.WriteString(l + "\n")
	}
	if p.dropped > 0 {
		fmt.Fprintf(&b, "[… %d more %s, in its log: local_output %s]\n", p.dropped, map[bool]string{true: "line", false: "lines"}[p.dropped == 1], p.id)
	}
	p.batch, p.bytes, p.dropped = nil, 0, 0
	ps.pushLocked(Event{ID: p.id, Text: strings.TrimRight(b.String(), "\n")}, true)
}

// wait waits for p to end and tells of it.
func (ps *Processes) wait(p *process) {
	<-p.run.done
	ps.end(p, "")
}

// end marks p ended, how (see process.how; "" when it exited by itself),
// stopping it first if it runs, and makes its exit event unless the model
// stopped it.
func (ps *Processes) end(p *process, how string) {
	ps.mu.Lock()
	if p.ended {
		ps.mu.Unlock()
		return
	}
	if how != "" && p.how == "" {
		p.how = how
	}
	ps.mu.Unlock()
	if how != "" {
		p.run.cancel()
	}
	<-p.run.done
	if p.linesDone != nil { // its last lines, unless a child it left holds the pipe
		select {
		case <-p.linesDone:
		case <-time.After(time.Second):
		}
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()
	if p.ended {
		return
	}
	p.ended, p.at = true, time.Now()
	if p.expiry != nil {
		p.expiry.Stop()
	}
	p.exit = -1
	if p.how == "" {
		p.exit = exitCode(p.run.err)
	}
	if p.monitor {
		ps.flushLocked(p)
	}
	p.log.Close()
	if p.quiet || ps.closed {
		return
	}
	ps.pushLocked(Event{ID: p.id, Text: ps.endText(p)}, p.how != "stopped by the user")
}

// exitCode is a finished command's exit code, -1 when it has none.
func exitCode(err error) int {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		return exit.ExitCode()
	}
	return -1
}

// endText tells how p ended, with the last lines of its output.
func (ps *Processes) endText(p *process) string {
	took := display.Duration(p.at.Sub(p.started))
	var what string
	switch {
	case p.how == "expired":
		what = fmt.Sprintf("expired after %s with %d %s; start it again with local_monitor if you still need it", timeoutText(p.timeout), p.sent, plural(p.sent, "event", "events"))
	case p.how == "flooded":
		what = fmt.Sprintf("was stopped: more than %d lines in %s; start it again with a filter that prints only the lines you would act on", floodLines, timeoutText(floodWindow))
	case p.how == "stopped by the user":
		what = "was stopped by the user after " + took
	case p.how != "":
		what = p.how + " after " + took
	case p.exit >= 0:
		what = fmt.Sprintf("exited with code %d after %s", p.exit, took)
	default:
		what = fmt.Sprintf("ended (%v) after %s", p.run.err, took)
	}
	name := p.command
	if p.monitor {
		name = fmt.Sprintf("monitor %q", p.desc)
	}
	text := fmt.Sprintf("%s · %s · %s", p.id, oneLine(name, 120), what)
	if p.monitor && p.how == "" {
		text += fmt.Sprintf(", after %d %s", p.sent, plural(p.sent, "event", "events"))
	}
	if !p.monitor {
		if tail := lastLines(p.logPath, exitTail); tail != "" {
			text += ". Last lines:\n" + tail
		} else {
			text += ". No output."
		}
	}
	return text
}

// push adds an event; wake schedules the model's wake-up (a stop by the
// user waits for the next message instead).
func (ps *Processes) pushLocked(e Event, wake bool) {
	ps.events = append(ps.events, e)
	if wake && ps.Wake != nil && ps.waking == nil {
		ps.waking = time.AfterFunc(eventDelay, func() {
			ps.mu.Lock()
			ps.waking = nil
			closed := ps.closed
			ps.mu.Unlock()
			if !closed {
				ps.Wake()
			}
		})
	}
}

// Drain returns the events waiting, and forgets them.
func (ps *Processes) Drain() []Event {
	if ps == nil {
		return nil
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	e := ps.events
	ps.events = nil
	return e
}

// Unread puts events back, first, without waking anyone: a turn that
// carried them failed, so they go with the next one.
func (ps *Processes) Unread(events []Event) {
	if ps == nil || len(events) == 0 {
		return
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.events = append(append([]Event(nil), events...), ps.events...)
}

// Stop stops process id with everything it started. user says the user
// stopped it (the model is told), else the model did (it knows).
func (ps *Processes) Stop(id string, user bool) (Info, error) {
	p := ps.find(id)
	if p == nil {
		return Info{}, ps.unknown(id)
	}
	ps.mu.Lock()
	ended := p.ended
	p.quiet = !user
	ps.mu.Unlock()
	if !ended {
		how := "stopped"
		if user {
			how = "stopped by the user"
		}
		ps.end(p, how)
	}
	return ps.info(p), nil
}

func (ps *Processes) find(id string) *process {
	if ps == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, p := range ps.list {
		if p.id == id {
			return p
		}
	}
	return nil
}

func (ps *Processes) unknown(id string) error {
	if ps.Wake == nil {
		return fmt.Errorf("no background process %q here: processes are stopped when an answer ends", id)
	}
	return fmt.Errorf("no background process %q in this chat (processes stop when the chat ends); local_output without an id lists them", id)
}

// Output is process id's status and the last n lines of its output.
func (ps *Processes) Output(id string, n int) (Info, string, error) {
	p := ps.find(id)
	if p == nil {
		return Info{}, "", ps.unknown(id)
	}
	return ps.info(p), lastLines(p.logPath, n), nil
}

// List describes every process of the run, oldest first.
func (ps *Processes) List() []Info {
	if ps == nil {
		return nil
	}
	ps.mu.Lock()
	list := append([]*process(nil), ps.list...)
	ps.mu.Unlock()
	out := make([]Info, len(list))
	for i, p := range list {
		out[i] = ps.info(p)
	}
	return out
}

// Running counts the processes still running.
func (ps *Processes) Running() int {
	n := 0
	for _, i := range ps.List() {
		if i.Running {
			n++
		}
	}
	return n
}

func (ps *Processes) info(p *process) Info {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	i := Info{ID: p.id, Command: p.command, Description: p.desc, Monitor: p.monitor, Running: !p.ended,
		Started: p.started, Ended: p.at, Exit: p.exit, How: p.how, Log: p.logPath}
	if p.monitor && !p.ended {
		i.Left = max(0, p.timeout-time.Since(p.started))
	}
	return i
}

// Close stops every process still running and returns how many it
// stopped. Events stop with it.
func (ps *Processes) Close() int {
	if ps == nil {
		return 0
	}
	ps.mu.Lock()
	ps.closed = true
	if ps.waking != nil {
		ps.waking.Stop()
	}
	var running []*process
	for _, p := range ps.list {
		if !p.ended {
			running = append(running, p)
		}
	}
	ps.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range running {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ps.end(p, "stopped")
		}()
	}
	wg.Wait()
	return len(running)
}

// Status says what a process is doing, in a few words: "running · 3m",
// "exited 0 · 2m ago", "monitor · 4m left".
func (i Info) Status() string {
	switch {
	case i.Running && i.Monitor:
		return "running · " + timeoutText(i.Left.Round(time.Second)) + " left"
	case i.Running:
		return "running · " + display.Duration(time.Since(i.Started))
	case i.How == "" && i.Exit >= 0:
		return fmt.Sprintf("exited %d · %s", i.Exit, display.Ago(i.Ended))
	case i.How == "":
		return "ended · " + display.Ago(i.Ended)
	}
	return i.How + " · " + display.Ago(i.Ended)
}

// Describe is a line about the process for the model.
func (i Info) Describe() string {
	what := "$ " + oneLine(i.Command, 120)
	if i.Monitor {
		what = fmt.Sprintf("monitor %q: %s", i.Description, oneLine(i.Command, 100))
	}
	return fmt.Sprintf("%s · %s · %s", i.ID, i.Status(), what)
}

// Format writes events for the model, with what still runs.
func (ps *Processes) Format(events []Event) string {
	var b strings.Builder
	b.WriteString("<local_process_event>\n")
	b.WriteString("From docsgpt-cli on the user's computer, not typed by the user: background processes of this chat (run_command with run_in_background, local_monitor). They are not server jobs; check_job and monitor_* don't know them.\n")
	for _, e := range events {
		b.WriteString(e.Text + "\n")
	}
	var running []string
	for _, i := range ps.List() {
		if i.Running {
			running = append(running, i.Describe())
		}
	}
	if len(running) > 0 {
		b.WriteString("Still running: " + strings.Join(running, "; ") + "\n")
	} else {
		b.WriteString("No background process runs now.\n")
	}
	b.WriteString("</local_process_event>")
	return b.String()
}

// lastLines returns the last n lines of the log at path, within the
// limits of a tool's output.
func lastLines(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	const window = 256 << 10
	start := max(0, fi.Size()-window)
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return ""
	}
	if start > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	text := display.StripControls(strings.ToValidUTF8(string(buf), ""))
	all := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(all) == 1 && all[0] == "" {
		return ""
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return truncateTail(strings.Join(all, "\n"), 0)
}

// oneLine shows a command on one line, cut at n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
