package mcp

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	docsgpt "github.com/arc53/DocsGPT-cli/sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// StartTimeout is how long a server has to start and list its tools.
	StartTimeout = 30 * time.Second
	// CallTimeout is how long a tool call may take.
	CallTimeout = 5 * time.Minute
	// maxNameLen keeps a tool's name within the providers' 64 characters
	// once the DocsGPT server has appended "_ct<N>" to it.
	maxNameLen = 58
)

// Tool is a server's tool as the agent sees it.
type Tool struct {
	Name   string // mcp__<server>__<tool>
	Server string
	Tool   *mcp.Tool // as the server describes it
}

// ReadOnly reports whether the server says the tool changes nothing.
func (t *Tool) ReadOnly() bool { return t.Tool.Annotations != nil && t.Tool.Annotations.ReadOnlyHint }

// Manager runs the MCP servers of a chat or an ask run.
type Manager struct {
	version string
	servers []*server
	cancel  context.CancelFunc // ends the starts still going on
	ready   chan struct{}      // closed once every server started or failed

	mu    sync.Mutex
	tools []*Tool // set once ready
}

type server struct {
	Server
	log     *stderrLog
	connect sync.Mutex // held while it starts

	mu           sync.Mutex
	starting     bool
	proc         *process
	session      *mcp.ClientSession
	tools        []*mcp.Tool
	instructions string
	err          error // why it did not start, or stopped
	started      bool  // connected once
	lost         bool  // the session ended (the server may live on)
	closed       bool
}

// Start starts the servers in the background; Wait waits for them. version
// is the CLI's, told to the servers.
func Start(servers []Server, version string) *Manager {
	base, cancel := context.WithCancel(context.Background())
	m := &Manager{version: version, ready: make(chan struct{}), cancel: cancel}
	var wg sync.WaitGroup
	for _, s := range servers {
		srv := &server{Server: s, log: openLog(s.Name), starting: true}
		m.servers = append(m.servers, srv)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(base, StartTimeout)
			defer cancel()
			srv.start(ctx, m.version)
		}()
	}
	go func() {
		wg.Wait()
		m.mu.Lock()
		m.tools = name(m.servers)
		m.mu.Unlock()
		close(m.ready)
	}()
	return m
}

// Wait waits until every server has started or failed, or ctx ends.
func (m *Manager) Wait(ctx context.Context) error {
	select {
	case <-m.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Starting reports whether a server is still starting.
func (m *Manager) Starting() bool {
	select {
	case <-m.ready:
		return false
	default:
		return true
	}
}

// Definitions are the servers' tools for a chat request, once Wait has
// returned (none before).
func (m *Manager) Definitions() []docsgpt.Tool {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	defs := make([]docsgpt.Tool, len(m.tools))
	for i, t := range m.tools {
		desc := t.Tool.Description
		if desc == "" {
			desc = t.Tool.Title
		}
		if len(desc) > 4096 {
			desc = strings.ToValidUTF8(desc[:4096], "") + "…"
		}
		defs[i] = docsgpt.Tool{Type: "function", Function: docsgpt.ToolFunction{
			Name: t.Name, Description: desc, Parameters: schema(t.Tool.InputSchema),
		}}
	}
	return defs
}

// Lookup finds the tool the model called by name.
func (m *Manager) Lookup(name string) *Tool {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tools {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// Call runs the tool with args. A server that stopped is started again
// first; the call itself is never repeated, as the server may have acted.
func (m *Manager) Call(ctx context.Context, t *Tool, args map[string]any) (*mcp.CallToolResult, error) {
	for _, srv := range m.servers {
		if srv.Name == t.Server {
			cs, err := srv.live(ctx, m.version)
			if err != nil {
				return nil, err
			}
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: t.Tool.Name, Arguments: args})
			if errors.Is(err, mcp.ErrConnectionClosed) || errors.Is(err, io.EOF) { // the server is gone
				srv.mu.Lock()
				srv.lost = srv.lost || srv.session == cs
				srv.mu.Unlock()
			}
			if err != nil && ctx.Err() == nil {
				return nil, srv.explain(err)
			}
			return res, err
		}
	}
	return nil, fmt.Errorf("no MCP server %s", t.Server)
}

// Close stops every server.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.cancel()
	var wg sync.WaitGroup
	for _, srv := range m.servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.stop()
		}()
	}
	wg.Wait()
}

// Status is how a server is doing, for the user.
type Status struct {
	Server
	Starting     bool
	Running      bool
	Err          error
	Tools        []string // their names on the server
	Instructions string   // what the server tells the agent about itself
	Log          string   // the file its stderr goes to
	Stderr       []string // its last lines there
}

// Status reports on every server started, in order.
func (m *Manager) Status() []Status {
	if m == nil {
		return nil
	}
	var out []Status
	for _, srv := range m.servers {
		st := Status{Server: srv.Server, Log: srv.log.path, Stderr: srv.log.last(5)}
		srv.mu.Lock()
		st.Starting, st.Err, st.Instructions = srv.starting, srv.err, srv.instructions
		st.Running = srv.session != nil && !srv.lost && !srv.proc.exited()
		if !st.Starting && st.Err == nil && !st.Running {
			st.Err = errors.New("stopped")
		}
		for _, t := range srv.tools {
			st.Tools = append(st.Tools, t.Name)
		}
		srv.mu.Unlock()
		out = append(out, st)
	}
	return out
}

// start starts the server and opens a session with it; the first time, it
// also lists the tools. It holds s.connect, so that two calls finding the
// server stopped start it once.
func (s *server) start(ctx context.Context, version string) error {
	s.mu.Lock()
	first, closed := !s.started, s.closed
	s.mu.Unlock()
	if closed {
		return errors.New("the MCP server was stopped")
	}
	s.log.mark("start %s (in %s)", s.CommandLine(), s.Dir)
	p, cs, tools, err := s.open(ctx, version, first)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starting, s.err = false, err
	if err != nil {
		return err
	}
	if s.closed { // stopped meanwhile
		cs.Close()
		p.stop()
		return errors.New("the MCP server was stopped")
	}
	s.proc, s.session, s.started, s.lost = p, cs, true, false
	if first {
		s.tools = tools
	}
	if r := cs.InitializeResult(); r != nil {
		s.instructions = r.Instructions
	}
	return nil
}

// open runs the command and connects to it, listing its tools with list.
func (s *server) open(ctx context.Context, version string, list bool) (*process, *mcp.ClientSession, []*mcp.Tool, error) {
	p, out, err := startProcess(s.Server, s.log)
	if err != nil {
		return nil, nil, nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "docsgpt-cli", Version: version},
		&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}}) // no roots, sampling or elicitation
	cs, err := client.Connect(ctx, &mcp.IOTransport{Reader: out, Writer: p.stdin}, nil)
	var tools []*mcp.Tool
	if err == nil && list {
		for t, lerr := range cs.Tools(ctx, nil) {
			if lerr != nil {
				err = fmt.Errorf("listing its tools: %w", lerr)
				break
			}
			tools = append(tools, t)
		}
	}
	if err != nil {
		if cs != nil {
			cs.Close()
		}
		p.stop()
		if ctx.Err() != nil {
			err = fmt.Errorf("it did not start within %s", StartTimeout)
		}
		return nil, nil, nil, s.explain(err)
	}
	return p, cs, tools, nil
}

// live returns the session, starting the server again when it stopped.
func (s *server) live(ctx context.Context, version string) (*mcp.ClientSession, error) {
	s.connect.Lock()
	defer s.connect.Unlock()
	s.mu.Lock()
	cs, p, started, closed, lost := s.session, s.proc, s.started, s.closed, s.lost
	switch {
	case closed:
		s.mu.Unlock()
		return nil, errors.New("the MCP server was stopped")
	case cs != nil && !lost && !p.exited():
		s.mu.Unlock()
		return cs, nil
	case !started:
		err := s.err
		s.mu.Unlock()
		return nil, err
	}
	s.session, s.proc, s.starting = nil, nil, true
	s.mu.Unlock()
	if cs != nil {
		cs.Close()
		p.stop()
	}
	ctx, cancel := context.WithTimeout(ctx, StartTimeout)
	defer cancel()
	if err := s.start(ctx, version); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session, nil
}

func (s *server) stop() {
	s.mu.Lock()
	s.closed = true
	cs, p := s.session, s.proc
	s.session, s.proc = nil, nil
	s.mu.Unlock()
	if cs != nil {
		cs.Close()
		p.stop()
		s.log.mark("stop")
	}
	s.log.close()
}

// explain adds the server's last words on stderr to err: what it wrote
// tells more than a closed connection.
func (s *server) explain(err error) error {
	if last := s.log.last(3); len(last) > 0 {
		return fmt.Errorf("%w; it wrote: %s", err, strings.Join(last, " | "))
	}
	return err
}

// name gives the servers' tools their names for the model:
// mcp__<server>__<tool>, cleaned to the characters providers accept, with
// a short hash when that is too long or taken.
func name(servers []*server) []*Tool {
	var out []*Tool
	taken := map[string]bool{}
	for _, srv := range servers {
		if srv.err != nil {
			continue
		}
		for _, t := range srv.tools {
			n := "mcp__" + clean(srv.Name) + "__" + clean(t.Name)
			if len(n) > maxNameLen || taken[n] {
				sum := sha1.Sum([]byte(srv.Name + "\x00" + t.Name))
				n = n[:min(len(n), maxNameLen-7)] + "_" + hex.EncodeToString(sum[:])[:6]
			}
			taken[n] = true
			out = append(out, &Tool{Name: n, Server: srv.Name, Tool: t})
		}
	}
	return out
}

// clean replaces what is not a letter, digit, _ or - with _.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, s)
}

// schema is a tool's input schema as providers take it: an object with
// properties, without $schema.
func schema(in any) json.RawMessage {
	m := map[string]any{}
	if given, ok := in.(map[string]any); ok {
		for k, v := range given {
			m[k] = v
		}
	}
	delete(m, "$schema")
	m["type"] = "object"
	if _, ok := m["properties"]; !ok {
		m["properties"] = map[string]any{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return b
}
