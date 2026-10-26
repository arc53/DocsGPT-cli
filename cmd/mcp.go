package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	ctxenrich "github.com/arc53/DocsGPT-cli/internal/context"
	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/mcp"
	"github.com/arc53/DocsGPT-cli/internal/tools"
	"github.com/arc53/DocsGPT-cli/internal/ui"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP servers whose tools the agent can use",
	Long: `MCP servers give the agent more tools, such as Laravel Boost's docs search
and database queries. The chat and one-shot questions start the servers
listed under "mcpServers" in ~/.docsgpt/mcp.json and in the project's
.mcp.json (in the working directory or above it, up to the git root), and
stop them when they end. Only servers run as a command (stdio) are supported.

  {
    "mcpServers": {
      "laravel-boost": { "command": "php", "args": ["artisan", "boost:mcp"] }
    }
  }

A project's servers start only once you have approved them, as the file has
them: you are asked the first time, and again when the entry changes. Each
tool call is approved like a command.`,
	Example: `  docsgpt-cli mcp list
  docsgpt-cli mcp reset`,
}

var mcpListCmd = &cobra.Command{
	Use:   "list",
	Short: "Start the MCP servers and list their tools",
	Long: `Start the MCP servers the chat would start here, list their tools, and
stop them. The project's servers you have not approved are listed, not
started. Exits 1 when a server fails to start.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		all, err := mcp.Load(cwd)
		if err != nil {
			return err
		}
		if len(all) == 0 {
			fmt.Println("No MCP servers in " + display.ShortPath(mcp.UserFile()) + " or a project .mcp.json.")
			return nil
		}
		var start []mcp.Server
		for _, s := range all {
			if s.Err == nil && !s.Disabled && mcp.Decided(s) == mcp.Approved {
				start = append(start, s)
			}
		}
		m := mcp.Start(start, Version)
		defer m.Close()
		ctx, stop := signalContext()
		defer stop()
		if err := waitServers(ctx, m); err != nil {
			return err
		}
		text, failed := describeServers(all, m.Status())
		fmt.Print(text)
		if failed {
			return &exitError{code: exitFailure, err: errReported}
		}
		return nil
	},
}

var mcpResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Ask again before starting the project's MCP servers",
	Long: `Forget your answers about the servers of the project's .mcp.json (Start or
Never), so that the next chat asks about them again.`,
	Args: usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		file := mcp.ProjectFile(cwd)
		if file == "" {
			fmt.Println("No .mcp.json here.")
			return nil
		}
		n, err := mcp.Forget(file)
		if err != nil {
			return err
		}
		fmt.Printf("Forgot %d %s about %s.\n", n, plural(n, "answer", "answers"), display.ShortPath(file))
		return nil
	},
}

func init() {
	groupCommand(mcpCmd)
	mcpCmd.AddCommand(mcpListCmd, mcpResetCmd)
}

// waitServers waits for the servers to start, saying so on a terminal's
// stderr. It fails only when ctx ends (Ctrl+C).
func waitServers(ctx context.Context, m *mcp.Manager) error {
	if m.Starting() && isatty.IsTerminal(os.Stderr.Fd()) {
		fmt.Fprint(os.Stderr, display.Muted("Starting MCP servers…"))
		defer fmt.Fprint(os.Stderr, "\r\x1b[2K")
	}
	if err := m.Wait(ctx); err != nil {
		if t := terminated(ctx); t != nil {
			return t
		}
		return &exitError{code: 130, err: errors.New("interrupted")}
	}
	return nil
}

// mcpServers loads the MCP servers and returns those to start: the user's,
// and the project's the user approved, asked through u when ask (else the
// undecided ones are left out). notes are what to tell the user: entries
// that cannot start, servers left out.
func mcpServers(u tools.UI, ask bool) (all, start []mcp.Server, notes []string) {
	cwd, _ := os.Getwd()
	all, err := mcp.Load(cwd)
	if err != nil {
		return nil, nil, []string{"MCP servers not started: " + err.Error()}
	}
	for _, s := range all {
		switch {
		case s.Disabled:
			continue
		case s.Err != nil:
			notes = append(notes, fmt.Sprintf("MCP server %s (%s) not started: %v", display.Safe(s.Name), display.ShortPath(s.File), s.Err))
			continue
		}
		switch mcp.Decided(s) {
		case mcp.Refused:
			continue
		case mcp.Undecided:
			if !ask {
				notes = append(notes, fmt.Sprintf("MCP server %s of %s not started: approve it in a chat here first", display.Safe(s.Name), display.ShortPath(s.File)))
				continue
			}
			if !approveServer(u, s) {
				continue
			}
		}
		start = append(start, s)
	}
	return all, start, notes
}

// approveServer asks whether to start the project's server s, and
// remembers a Start or Never.
func approveServer(u tools.UI, s mcp.Server) bool {
	// The command is the title, which wraps: it must show whole.
	u.Open(display.Safe("$ "+s.CommandLine()), "(MCP server "+display.Safe(s.Name)+" of "+display.ShortPath(s.File)+")")
	var lines []string
	for _, set := range s.Settings() {
		lines = append(lines, display.Safe(set))
	}
	u.Lines(append(lines, "in "+display.Safe(display.ShortPath(s.Dir))))
	choice, err := u.Choose([]ui.Item{
		{Label: "Start", Value: "start", Keys: []string{"s", "y"}, Description: "start it now, and from now on while its entry stays the same"},
		{Label: "Not now", Value: "skip", Keys: []string{"n"}, Description: "don't start it this time, ask again next time"},
		{Label: "Never", Value: "never", Keys: []string{"v"}, Description: "don't start it, and don't ask again (docsgpt-cli mcp reset asks again)"},
	})
	if err != nil {
		choice = "skip"
	}
	switch choice {
	case "start":
		if err := mcp.Remember(s, true); err != nil {
			u.Close(true, "started, but the approval was not saved: "+err.Error())
		} else {
			u.Close(true, "approved")
		}
		return true
	case "never":
		mcp.Remember(s, false)
		u.Close(false, "never started")
	default:
		u.Close(false, "not started")
	}
	return false
}

// mcpContext describes the running servers for the context block, once
// all have started (their tools are offered from then on).
func mcpContext(m *mcp.Manager) []ctxenrich.MCPServer {
	if m == nil || m.Starting() {
		return nil
	}
	var out []ctxenrich.MCPServer
	for _, st := range m.Status() {
		if len(st.Tools) > 0 {
			out = append(out, ctxenrich.MCPServer{Name: st.Name, Tools: st.Tools, Instructions: st.Instructions})
		}
	}
	return out
}

// mcpFailures are the errors of the servers that did not start.
func mcpFailures(m *mcp.Manager) []string {
	var out []string
	for _, st := range m.Status() {
		if st.Err != nil {
			out = append(out, fmt.Sprintf("MCP server %s did not start: %v (log: %s)", display.Safe(st.Name), st.Err, display.ShortPath(st.Log)))
		}
	}
	return out
}

// describeServers lists the servers, started or not, with their tools; it
// reports whether one failed.
func describeServers(all []mcp.Server, statuses []mcp.Status) (text string, failed bool) {
	var b strings.Builder
	byName := map[string]mcp.Status{}
	for _, st := range statuses {
		byName[st.Name] = st
	}
	for _, s := range all {
		name, from := display.Safe(s.Name), display.Dim(" · "+display.ShortPath(s.File))
		st, started := byName[s.Name]
		switch {
		case s.Disabled:
			fmt.Fprintf(&b, "%s %s%s\n", display.Dim("-"), name, display.Dim(" · disabled")+from)
		case s.Err != nil:
			failed = true
			fmt.Fprintf(&b, "%s %s%s\n    %s\n", display.Danger("✗"), name, from, display.Safe(s.Err.Error()))
		case !started && mcp.Decided(s) == mcp.Refused:
			fmt.Fprintf(&b, "%s %s%s\n", display.Dim("-"), name, display.Dim(" · never started (docsgpt-cli mcp reset asks again)")+from)
		case !started:
			fmt.Fprintf(&b, "%s %s%s\n", display.Dim("-"), name, display.Dim(" · not approved yet: a chat here asks")+from)
		case st.Starting:
			fmt.Fprintf(&b, "%s %s%s\n", display.Dim("…"), name, display.Dim(" · starting")+from)
		case st.Err != nil:
			failed = true
			fmt.Fprintf(&b, "%s %s%s\n    %s\n    %s\n", display.Danger("✗"), name, from, display.Safe(st.Err.Error()),
				display.Dim("log: "+display.ShortPath(st.Log)))
		default:
			fmt.Fprintf(&b, "%s %s%s\n", display.Success("✓"), name, display.Dim(fmt.Sprintf(" · %d %s", len(st.Tools), plural(len(st.Tools), "tool", "tools")))+from)
			if len(st.Tools) > 0 {
				fmt.Fprintf(&b, "    %s\n", display.Safe(strings.Join(st.Tools, ", ")))
			}
		}
	}
	return b.String(), failed
}
