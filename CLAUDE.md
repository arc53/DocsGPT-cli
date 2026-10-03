# DocsGPT-cli

Go CLI for DocsGPT: chat with agents from the terminal, one-shot questions in
pipelines, agents and sources as code, bench, host mode. User docs: `README.md`
(short) and `docs/` (one page per area; update the page when behaviour changes).
Development rules: `AGENTS.md`.

## Project structure

```
cmd/docsgpt-cli/     → Entry point (package main), calls cmd.Execute(). Lives here
                       so `go install .../cmd/docsgpt-cli@latest` names the binary
                       docsgpt-cli rather than DocsGPT-cli (the module path's last
                       element). Blank-imports internal/earlytheme, which pins
                       lipgloss's background before bubbletea's init would query
                       the terminal (5s stall where it never answers); it runs
                       first by import-path order, checked by main_test.go
sdk/                 → SEPARATE Go module github.com/arc53/DocsGPT-cli/sdk, package
                       docsgpt: the public chat client (Client, Send, SendStream,
                       RunWithTools + RunOptions/RunResult, Models, StreamHandler,
                       Source, APIError with Message/Code/RetryAfter, RetryPolicy:
                       Client.Retry, 3 retries 2s/4s/8s of a chat request that failed
                       before its response — 408/429/502-504, refused/reset/timeout,
                       Retry-After capped by MaxDelay, x-should-retry, never once a
                       stream has started; a blocking OnRetry counts towards the wait)
                       — OpenAI-compatible types and the tool-call loop;
                       results carry conversation id, sources, model and usage. A stream
                       without [DONE] and without a finish_reason is an error, and so is a
                       tool_calls index outside 0-63. Stdlib-only, tagged sdk/vX.Y.Z
                       independently of the CLI, currently pre-v1. The CLI depends
                       on it through a pinned require in go.mod (imported as
                       `docsgpt "…/sdk"`, since the package name is not the last
                       path element); go.work points local builds at ./sdk, and
                       release builds set GOWORK=off to use the pinned version.
                       sdk/README.md is the module's user doc
docs/                → user docs: install (+ updating), quickstart, chat, tools,
                       configuration, agents-as-code, sources, bench, host, ci;
                       docs/README.md is the index
examples/            → agents/ (sample definition), bench/ (suite, one case per
                       feature), ci/github-actions.yml
deployment/          → install.sh / install.ps1 (attached to every release)
cmd/
  root.go            → Root command = the entry point (chat on a TTY, else one-shot ask), typo guard + questionArgs (extra words after a command: exit 2 with `To ask it as a question: docsgpt-cli -- "…"`), global flags (--url, --key, --token), chat flags (+ --no-stdin), help groups + usage template, Execute (startup config, update gate)
  ask.go             → Single-shot Q&A with streaming + tool support (hidden alias; the root runs it); --json (askResult on stdout, errors in it + errReported exit)
  errors.go          → explainChatError: one actionable line per failure (401/403 → login, 404 → url, 429, 5xx, can't reach: refused/DNS/TLS/timeout); DOCSGPT_DEBUG=1 adds the raw error
  retry.go           → retrying(): a per-run client copy whose retry waits count down on a status ("Retrying (1/3) in 4s… (502 Bad Gateway)"); the retry setting
  completion.go      → cobra's completion command (under Tools) + config-only completions: key names, settings and their values; completing() keeps __complete off the update gate
  whatsnew.go        → chat: the release notes once after an update (first 10 lines, whats_new setting), /changelog
  chat.go            → Interactive chat (hidden alias; optional first message): drives the full-screen ui.Screen from a goroutine (loop over Screen.Next), the slash command table, !cmd, sessions (-c/-r//resume), screenTools (tools.UI in the transcript and panel), transcript printed on exit
  config.go          → config get / set / show / path + the settings menu; one `settings` table drives all of them
  install.go         → Hidden `install` (run by the install scripts), wiring over internal/install
  update.go          → Self-update to latest GitHub release (--check, --yes/-y: needed off a terminal, else ui.Confirm; --rollback, hidden --worker)
  host.go            → host daemon commands (pair [code]: ui.Input validated by the redeem call, then a ui.Select of start / install / nothing on a TTY, first stdin line otherwise; status, revoke, reset, install-/uninstall-service); wiring over internal/host
  bench.go           → Benchmark suites vs agents (bench / bench record / bench init; --model, --matrix, --run-tag, --agent-id)
  manage.go          → Exit codes (0/1/2, exitError/usageErr), PAT client construction, confirmations
  login.go           → login / logout / whoami / hidden keys: agent API keys + the PAT, the key picker, first-run key prompt (chatKey)
  agents.go          → agents list / export / plan / apply / delete (agents as code), agents trigger (incoming webhook); flags + output only, the flows live in internal/manage
  sources.go         → sources list / upload / delete, agents prompts / agents tools (old `prompts list` / `tools list` hidden)
  utils.go           → printError, codeBlocks/extractCommand, copyToClipboard, signalContext (INT/TERM/HUP cancel with a ui.Signal cause) + terminated
internal/
  earlytheme/        → pins lipgloss's background before bubbletea's init (see cmd/docsgpt-cli)
  config/
    config.go        → Unified config load/save/migrate from ~/.docsgpt/config.json; key/token/URL resolution (flag > env > config, ErrNoKey), key and token redaction
  install/           → PATH install of the running binary (profiles, Windows user PATH), IsWritable
  bench/
    spec/            → Suite/case YAML format (bench.yaml + case.yaml), loading, validation, golden files; model/stream/attachments_mode/turns/expect.error/expect.stream
    assert/          → Assertion engine: answer/json (gjson paths)/sources/tools/limits (incl. TTFT)/stream integrity/error (negative cases)/golden matchers
    target/          → Four wire protocols: v1 (/v1/chat/completions, JSON or SSE, inline file parts), stream (/stream SSE, TTFT + frames), answer (/api/answer), webhook (+ /api/task_status polling); attachment upload; ServerError for negative cases
    judge/           → LLM-as-judge grading via a second agent (model/temperature passthrough)
    pricing/         → Cost table: /api/models pricing (tolerant field reader) + bench.yaml pricing overrides
    runner/          → Worker pool, repeat/min_pass, fail-fast, golden record, judge wiring, multi-turn loop, model override, cost stamping, RunMatrix (--matrix)
    report/          → Pretty/JSON(schema 2)/JUnit output, A/B compare, --matrix table + JSON, baselines in ~/.docsgpt/bench/<suite>/
  manage/
    client.go        → PAT HTTP client (Bearer dgpt_pat_…, docsgpt-cli/<ver> User-Agent, context timeouts), typed APIError (message, error code, required_scope)
    agents.go        → /api/user/me, get_agents, export_agent, import_agent/plan + import_agent (Plan, Resolution, ApplyResult), delete_agent
    sources.go       → /api/sources, /api/upload (multipart, Idempotency-Key, deterministic default key), /api/task_status polling with backoff, delete_old; Ingest = upload → --wait → --replace
    catalog.go       → get_prompts, get_tools
    webhooks.go      → agent incoming webhooks: URL parsing/redaction (Webhook), /api/agent_webhook lookup, webhook POST (no Authorization, Idempotency-Key), agent run result decoding
    trigger.go       → agents trigger flow: webhook resolution, anonymous --wait with same-origin PAT fallback, ReadPayload, UsageError (exit 2)
    documents.go     → -f expansion: files / directories / stdin, verbatim multi-document YAML splitting, kind: Agent validation
    resolve.go       → --resolve parsing, mapping onto the server `resolution` object, missing/unavailable gating
  host/
    config.go        → ~/.docsgpt/host.yml (device id, base URL, poll interval, log file; flat YAML, 0600); identity.go: Ed25519 host.key + request signing
    daemon.go        → RunDaemon: poll/SSE loop, idle heartbeat, idle-only auto-update + restart (revoke → ErrRevoked, exit 0); banner.go startup lines, servicemode.go + console_*.go: Windows task logging to host.log
    install.go       → InstallService / UninstallService over systemd (service.go), launchd (launchd.go), Task Scheduler (wintask.go)
    transport.go     → Signed polling + SSE session transport; invocation.go runs and streams commands (approval decided server-side; its own denylist floor); pairing.go, device.go, revoke.go
  context/
    enricher.go      → the <context> block: cwd, capped listing, AGENTS.md/CLAUDE.md (git root → cwd, 12KB), shell history (opt-in)
  session/
    session.go       → saved chats: ~/.docsgpt/sessions/<slug>-<hash>/<time>_<id>.jsonl (slug = last ≤48 chars of the cwd, non-[A-Za-z0-9_] runs as `-`; hash = 8 hex of sha256(cwd); 0600, dirs 0700); header + message/state lines, each Record one append; List keeps the files whose header cwd is this cwd; Load/Turns
  display/
    theme.go         → semantic palette (pi's OKHSL tones: hex + 256 + 16-color fallbacks,
                       dark/light; plain under NO_COLOR; tool block backgrounds a step
                       fainter than pi's, no 16-color value); also sets ui.Colors. InitTheme
                       runs in the root PersistentPreRun (hidden --theme > config > auto)
    renderer.go      → StreamRenderer: streamed markdown on a TTY (finished blocks
                       rendered once into scrollback, the block in progress redrawn
                       in place while it fits on screen); raw text when not a TTY.
                       Both strip control sequences first (StripControls, stateful
                       across chunks; non-TTY too, as piped output often lands on a
                       terminal). Wait() = "Thinking…" spinner (stderr) until the first visible
                       token; visible reasoning is a dim italic block of its own
    markdown.go      → glamour style built from the palette (no margins/fills); top-level
                       code fences drawn by us (dim ``` lines, 2-space indent, chroma
                       tokens colored from the palette). Glamour never wraps prose (its
                       wrapper miscounts hyphens, then re-wraps: orphan words/commas,
                       quote rows without a bar): goldmark + glamour's ANSI renderer at
                       width 0 with a `layout` AST pass (soft breaks → spaces, a list
                       item's later paragraphs on new lines, loose items a blank line
                       apart), then `rewrap` wraps at spaces only (wrapWords: words
                       never split unless wider than a row, styles carried per row)
                       under indent / quote bars / list hang. Unindented tables go to
                       a glamour renderer at the width. Links (incl. autolinks, bare
                       URLs) are marked in the AST (`markLinks`: U+FDD0/FDD1 emoji
                       nodes around the text, destination → "#" so glamour prints no
                       URL), then `placeLinks` swaps the markers for OSC 8 (ST) or a
                       dim ` (url)`, and `relink` makes each wrapped row open/close
                       its own links
    links.go         → hyperlinks: setting `hyperlinks` auto|on|off (SetHyperlinks in
                       the root PersistentPreRun) < DOCSGPT_HYPERLINKS=0|1|auto; auto =
                       pi's list (kitty, Ghostty, WezTerm, Warp, iTerm2 incl.
                       LC_TERMINAL over ssh, Windows Terminal, Alacritty, VS Code, Zed;
                       tmux only when `client_termfeatures` has hyperlinks; screen,
                       Terminal.app, unknown off). linkURL: http(s)/mailto, printable
                       ASCII (non-ASCII escaped via net/url), else not a hyperlink.
                       The model's own sequences are gone (StripControls) and the
                       markers dropped from its text before parsing
    chrome.go        → one dim header line (docsgpt · key · host · cwd) for ask; chat:
                       chatHeader (2-row mark + version, key hints, `Context` files),
                       ChatFooter (cwd + git branch read from .git/HEAD, worktrees and
                       detached too); the user-message block, Ago
    blocks.go        → the chat transcript's ui.Blocks, each rendered at the width and
                       cached per width (`fit` cuts over-wide rows, continuation rows
                       indented like the line): Header (banner once + chatHeader),
                       User (a ui.Prompt), Markdown, Sources, Note/Failure/Done/Text;
                       Answer (streamed: reasoning dim italic on top when shown, the
                       finished markdown blocks rendered once via commitPoint, the
                       last one again per frame; one glamour renderer kept per width);
                       ToolBlock (title + note, preview, output, ✓/✗ status, on
                       toolBox rows tinted by the outcome; keeps the output the
                       model gets, 2000 lines / 50 KB, lines cut to their last
                       4 KB; a ui.Expander: collapsed the last 5 lines under
                       `… N earlier lines · ctrl+o to expand`, expanded all kept)
    style.go         → Accent/Muted/Dim/Success/Warn helpers, ErrorMsg (stderr)
    tool.go          → tool blocks on stderr: bold title, status line (✓/✗), TailView
                       (live last-5-lines region), DiffPreview for writes. With
                       colors and stderr a 256/truecolor TTY a block is a toolBox:
                       full-width rows on colToolBg (pi's Box(1,1): padding row
                       above/below, one column each side; resets re-apply the bg),
                       the live tail keeps the bottom padding, and ToolStatus
                       redraws the on-screen part on colToolOkBg/colToolFailBg
                       (skipped if the width changed). Titles wrap there (rows are
                       cut to the width). Plain (indented, no bg) otherwise
    safe.go          → Safe: control chars as ␛ ␍ ␊, format runes as \uXXXX, so an
                       escape sequence cannot hide part of a command. Every model/server
                       string in tool blocks, sources, agents/sources lists, whoami, host
                       pair/status and bench errors goes through it (cmd's textOrDash for
                       table cells; --json stays raw).
                       StripControls: removes ESC/OSC/DCS/C1 sequences whole and other
                       controls but \n \t, for text read as text (answers, resumed
                       chats, the user-message block, /export, error messages)
    copy.go          → how the chat's blocks copy (ui.Plain): joins, markdownPlain,
                       unwrapped (see chat command, Selection)
    sources.go       → dim numbered "Sources" block, OSC 8 links (linkURL, http(s)
                       only; unless hyperlinks are set off), TTY only
    background*.go   → auto theme: COLORFGBG, else one OSC 11 query (stdout TTY only,
                       150ms max); the answer also feeds glamour and lipgloss
    banner.go        → dino banner, top of the chat's transcript, default "once"
  ui/                → inline bubbletea prompts: Select (list / inline row that stops at
                       its ends, filter, key shortcuts), Confirm/ConfirmWith, Input
                       (mask, validate), Spinner (stderr); TERM/HUP end a prompt with
                       ui.Signal; Stderr option for prompts drawn while stdout carries
                       an answer; Prompter (Inline, or a Screen's panel).
                       screen.go: the chat's full-screen program (see chat command);
                       selection.go: mouse selection; plain.go: Plain/Plainer,
                       Unwrap (how lines copy);
                       clipboard.go: Copy (system clipboard, else OSC 52);
                       editor.go: its input (editorModel, embedded); keys.go: ttyInput
                       (keyboard protocol → legacy bytes), NewlineKey; history.go:
                       prompt history file; fuzzy.go: popup/filter matching;
                       suspend_*.go: Ctrl+Z; hold_*.go: echo off while ask streams,
                       DiscardInput
  tools/
    definitions.go   → Tool schemas: run_command, read_file (offset/limit), write_file
    approval.go      → Session: per chat session / ask run; title, approval, execution,
                       status per call, shown and asked through Session.UI. Approve /
                       Always allow / Always approve (sets AutoApprove) / Deny / Edit;
                       readReason (when a read asks; home compared with os.SameFile),
                       secretNames
    allow.go         → alwaysKey: what "Always allow" covers, and when it is never offered
                       (its doc comment is the source of truth for the rules)
    executor.go      → runCommand (caller's ctx + timeout; own session without a
                       controlling terminal, whole group killed; Windows: hidden console,
                       taskkill /T, NoDefaultCurrentDirectoryInExePath=1 so cmd.exe
                       never runs a program from the working directory), regularFile + readFile (regular files only, chunked,
                       ctx-aware line ranges), writeFile (creates parents)
    ui.go            → UI: Open/Lines/Output/Close a block, Choose, Edit; stderrUI (the
                       default, ask): display's stderr blocks + inline prompts
    shell.go         → RunShell: the chat's `!cmd` (no approval, no time limit)
    procgroup_*.go   → own process group / session, whole-tree kill (Unix), taskkill (Windows)
    truncate.go      → model-bound output: last 2000 lines / 50KB, line/rune safe, with
                       a note about the dropped head; tailBuffer bounds the capture
  update/
    update.go        → GitHub latest-release lookup, semver comparison, mode constants
    apply.go         → Asset download, sha256 verify, binary swap + backup, Rollback, host CheckAndApply
    stage.go         → Staged updates in ~/.docsgpt/staging (download now, apply next launch)
    worker.go        → Detached background worker (`update --worker`): check + stage
    notify.go        → Check state in ~/.docsgpt/update_check.json (latest, skip version, latest release notes, whats_new = version an update installed)
    notes.go         → MarkUpdated (Apply/ApplyStaged) / PendingNotes / ShownNotes, LatestNotes (cache < 1 day, else fetch, else stale cache), TidyNotes (drops GoReleaser's heading, merges, hashes)
    restart_*.go     → Post-update restart: exec(2) on Unix, exit(3) on Windows
    detach_*.go      → Platform detach for the worker process
```

## How it works

### Entry point
`docsgpt-cli` with no args, stdin and stdout TTYs → chat; anything else (args, piped
stdin, stdout redirected) → ask. Subcommands win over questions. `commandTypo`
rejects (exit 2, with a `-- <words>` hint) a lone bare word that is a prefix or a
2-edit near miss of a command, or such a word followed by one of that command's
subcommands ("agnets list"); a quoted multi-word question or anything after `--` is
never checked. Group commands (`agents`, `sources`, `config`, `host`) reject unknown
subcommands the same way (`subcommandArgs`). Only `--url/--key/--token` are global;
`--no-stream/--no-context/--no-tools/--auto-approve/--tool-timeout` live on the root,
ask and chat, `--no-stdin` on the root and ask (`--timeout` is a deprecated alias on ask/chat; `--theme`/`--no-motion`
hidden); `-c/--continue` and `-r/--resume` on the root and chat open the chat (a TTY
is required; arguments become its first message).
Root `SilenceUsage` + flag error func: usage errors exit 2 everywhere, runtime errors
print no usage; `ui.ErrCancelled` exits 1 without a message.

### Credentials (login / logout / whoami)
Agent key: `--key <name>` > `DOCSGPT_API_KEY` > `default_key`. `login` on a TTY: a
filterable picker of stored keys (choose = make default) + "Add a key or token…" →
masked `ui.Input` validated behind a spinner: `dgpt_pat_…` → `GET /api/user/me`
(PAT flow below), else `GET /v1/models` with the key (no tokens spent; 401/403 =
rejected, 404 = old server, stored unverified) → name prompt (default: slug of the
agent name, next free) → "Make it the default?" unless it is the first key. Piped
stdin (no TTY): key or token on the first line, `--name`, a new key becomes the
default. A key login also stores the base URL, unless a PAT is stored for another
server (warning instead). With no key, ask/chat on a TTY run the same prompt inline
(`chatKey`, keys only) and continue; off a TTY: "No API key…" exit 1. `logout
[name] | --token | --all` (picker on a TTY; `ui.Confirm`, or `--yes` off a TTY;
`--token` shadows the global one, so a `dgpt_pat_…` given to it, `=` or as the
argument, names the stored token and must match it — `tokenSwitch`);
removing the default promotes the first remaining key. `whoami`: active key
(name, redacted, server, agent via /v1/models) + PAT identity; `--json` = PAT doc only;
neither configured (`--json`: no PAT) = exit 1, not a usage error.
`keys` is a hidden alias of the TTY picker (the old add/set/delete flags are gone).

### ask command
1. Unless `--no-stdin`, reads stdin to EOF when it is not a character device (a TTY, /dev/null): pipes, sockets, files (`readPipedStdin`), at most 1 MB (cut with a stderr note). Alone it is the question; with args it is appended as `<stdin>…</stdin>`. After 1s without data (`stdinHint`), a TTY stderr shows `waiting for piped input… (--no-stdin to skip)`: ssh without -t, CI runners that keep stdin open and `while read` loops need `--no-stdin` (or `</dev/null`)
2. Loads config from `~/.docsgpt/config.json`, resolves API key (Bearer auth, first-run prompt via `chatKey`) and base URL
3. Unless `--no-context`, prepends the `<context>` block (see chat)
4. Sends to `POST {base_url}/v1/chat/completions` with streaming
5. Handles tool calls (run_command, read_file, write_file) with user approval loop; tools are only offered when stdin is a TTY or `--auto-approve` is set. Tool UI (titles, approval prompt, command output, status) goes to stderr
6. stdout not a TTY: stdout carries only the answer, control sequences stripped (no header, sources, clipboard). On a TTY: header, rendered answer, dim `Sources` block, first bash/sh block copied to the clipboard (with a dim note)

Errors (every command) go to stderr; a missing question or a bad flag is a usage error (exit 2, flags with a `--help` pointer); Ctrl+C exits `ask` with 130; TERM and HUP cancel ask and chat like Ctrl+C (so the deferred terminal restores run) and exit 128 + the signal (143, 129): `signalContext` cancels with a `ui.Signal` cause, prompts return `ui.Signal` (bubbletea's own handler is off: it would submit the prompt on TERM), and `exitCodeFor` maps it.

### chat command
One bubbletea program for the whole chat, `ui.Screen`, on the alternate screen
(like pi's fullscreen mode); `runChat` runs it on the main goroutine and the chat
logic (`loop`: -r/-c/header, then `Screen.Next` → `handle`) on another, which
drives it through methods that send closures into the program (`doMsg`): Add,
Clear, Changed (one redraw per ~33ms), Busy/Status (spinner + what Esc/Ctrl+C
cancel), Footer, Select/Input (blocking, in the panel), Mouse, Quit. Not on a TTY
or with TERM=dumb it refuses.
- Layout, bottom up: footer, editor (dim rules; at most 30% of the height), the
  panel (a Select or Input, above the editor, which keeps its text but loses its
  cursor), the queued messages (`Queued: …` a row each, the first 3, then `↳ alt+↑
  to edit`; in a short window only `N queued` in the status row), one status row
  (spinner `Thinking…`/`Answering…`/`Running…` · `esc to stop`; right: `↓ N new
  lines · end to jump` when scrolled back),
  and the transcript filling the rest: the blocks (`ui.Block`, see display's
  blocks.go) a blank line apart, top-aligned, only the visible rows sliced into
  the frame. Every write is one synchronized update (`termOutput`, CSI ?2026).
- Scrolling: `top` + `follow` (at the end, the view follows; scrolled back it
  stays, `leftAt` counts new lines). Wheel via SGR mouse (bubbletea cell motion,
  the only mode it manages and restores): 1 line per event on macOS (the system
  accelerates, one event per line, pi's finding), else 3, 1 in bursts under 5ms.
  PgUp/PgDn (page − 1; a list panel keeps them), Shift+↑/↓ a line, Ctrl+↑/↓ to the
  previous/next `ui.Prompt` block (the user's messages), Home/End with an empty
  editor and no panel, Ctrl+Home/Ctrl+End always. Sending, and a panel opening,
  return to the end. A resize keeps a scrolled-back view on the same block, at
  the same share of it (blocks re-render at the new width).
- Ctrl+O (pi's app.tools.expand): `expanded` toggles every `ui.Expander` block
  (ToolBlock; reasoning stays with /think), new ones are added in that state;
  a scrolled-back view keeps its top line, or goes to the start of the block
  at its top when that block folds; flash `Tool output expanded/collapsed`.
- Selection (`ui/selection.go`, pi's fullscreen behaviour): with the mouse on,
  a left press in the transcript starts it, drag events (cell motion, 1002)
  extend it, the release copies it (`ui.Copy`, in a tea.Cmd) and flashes
  `Copied N characters` in the status row (2s; errors 5s). 2/3 presses within
  500ms on the same word: word (letters/digits/_ joined by / and -, . and '
  inside) / logical line. Points are transcript (line, col), so it stays on its
  text while streaming; the top row (reached from below) or under the
  transcript scrolls a line per 50ms tick while held. Highlight: reverse video,
  re-set after every SGR, grapheme-aligned (wide chars whole). A click, Esc
  (before anything else it would do), a resize, Clear or mouse off clear it.
  A click (no drag) on an OSC 8 link opens it (`openURL`: open / xdg-open /
  rundll32; not over SSH) and flashes `Opened <url>`, as pi does: the
  terminal cannot while the chat holds the mouse.
  Copy text: `ui.Unwrap` over the rows' `ui.Plain` (Indent = decoration columns
  left out, Wrap + Sep = a soft wrap and what it took out); blocks implementing
  `ui.Plainer` supply it, others copy as shown. display finds it in `copy.go`:
  `joins` matches each row's text into the block's unwrapped text (a message's
  source; markdown rendered by `unwrapped` at its longest paragraph's width +
  64, at copy time only, cached per version) — a row that follows the row
  above across only whitespace is a wrap, with that whitespace as Sep (so
  hyphen and `.` breaks and fit-cut code join exactly); `markdownPlain` adds
  the code indent (2, between our fences) and quote bars. Tables copy as shown.
- Clipboard (`ui.Copy`, also /copy and ask): system clipboard (atotto), else or
  over SSH (`SSH_TTY`/`SSH_CONNECTION`/`SSH_CLIENT`) OSC 52 written through the
  screen's `termOutput` (base64 capped at 100 000 bytes → an error); tmux needs
  `set-clipboard on`.
- Input: the editor always takes keys (typing while an answer streams); Enter while
  the chat is busy queues the message (sent after; Esc/Ctrl+C put the queue back
  into the editor; Alt+↑ does too without stopping: `requeue`, a blank line apart,
  before the draft, pastes kept collapsed and renumbered, one undo step). Esc or Ctrl+C with something cancellable running cancels it;
  idle, Ctrl+C clears, twice within 1s quits, Ctrl+D on empty quits (also while
  busy). A panel takes the keys only 300ms after it opened, so keys typed just
  before an approval appeared go to the editor. Ctrl+Z suspends (tea.Exec of a
  `handoff`), Ctrl+G runs $EDITOR the same way, Ctrl+L repaints.
- Terminal modes: alt screen, bracketed paste and mouse by bubbletea; `keysOn`
  (kitty flags 1 + modifyOtherKeys 1) written in Init, after the alt screen is up
  (kitty keeps a flag stack per screen), and after every handoff with the mouse
  (bubbletea's RestoreTerminal re-enables neither); `keysOff` in each handoff and
  after Run. TERM/HUP (own handler) quit the program and come back as `ui.Signal`;
  a `kill -INT` interrupts like the key. A panic in the chat goroutine kills the
  program (terminal restored) and re-panics on the main goroutine.
- Exit: after Run, the transcript (only if something was asked or resumed) is
  rendered at the final width and printed to the normal screen (not after HUP), then
  `Continue this chat with: docsgpt-cli -c` when saved.
- Keys of the editor: ttyInput translates `CSI code;mods u` and `CSI 27;mods;code ~`
  back to legacy bytes (Shift+Enter → `\n` = ctrl+j, Ctrl+C → 0x03, Esc → ESC, …),
  holds an unfinished CSI (keys, SGR mouse reports) across reads, passes bracketed
  pastes through untouched.
- Editor: dim rules above and below the text, a dim footer (left `~/dir (branch)`,
  right `key · host`, then `+N command outputs`, `think on`, then `auto-approve` in the warning color; it is dim as a whole,
  so a differently styled item goes last; the left is cut from its start).
  Enter sends; Shift+Enter / Ctrl+J / Alt+Enter / a trailing `\` insert a newline; rows wrap
  after a space (`wrapLine`: a longer word where it reaches the edge, a paste marker
  never split, a space may hang into the cursor column); ↑/↓ move by visual row, history at the edges
  (`~/.docsgpt/history`, JSON string per line, 0600, 500 entries, entries ≤16KB and
  not matching `secretLike`; trimming writes a temp file and renames it); bracketed pastes >10 lines or >1000 chars become
  `[paste #N +L lines]` markers (one unit: the cursor never rests inside one, any
  deletion that reaches into one removes it whole; expanded on send, shown
  collapsed in the transcript); Ctrl+A/E/K/U/W, Alt+←/→; Ctrl+G opens $VISUAL/$EDITOR.
  Ctrl+- (0x1f = ctrl+_; keys.go maps kitty/modifyOtherKeys Ctrl+- and Ctrl+/ to
  it) undoes, pi's way: a stack of (lines, cursor, pastes) states, 100 deep,
  cleared on send; a typed word is one step with the space before it, a run of
  the same deleting key one step, a history recall one step back to the draft,
  the $EDITOR result one step; no redo (pi has none).
  Typing `/` opens the command popup (prefix then fuzzy matches, under the editor); Tab
  completes, Enter runs the exact or selected command at once, Esc closes it.
- Commands (`chatCommands`, one table for popup, /help, dispatch): /new (/clear:
  the transcript starts over), /resume, /copy (whole answer, or a pick of its code
  blocks), /export [file] (markdown, default `docsgpt-<date>.md`; `~/` expanded; an
  existing file only after a confirmation, default No), /think, /approve (toggles
  the tools Session's AutoApprove; "Always allow" choices are kept), /key (switch
  or add a key → new conversation), /settings (the config menu in the panel; the
  mouse setting applies at once), /changelog (latest release notes), /help, /quit (/exit). An unknown `/word` is an
  error; `/path/like …` is a message. `!cmd` runs through `tools.RunShell` (no
  approval, no time limit, cancellable) into a tool block and its output is
  prepended to the next message; `!!cmd` is not sent.
- Sending: a `display.User` block, then `RunWithTools` with the messages +
  `conversation_id`; the server then takes the history from the stored
  conversation, an older one from the messages. Each answer segment is a
  `display.Answer` block (a new one after every tool call); tool calls go through
  `screenTools` (a ToolBlock per call, approvals as an inline Select in the panel).
  A failed or interrupted turn is dropped (`Interrupted.` note); a failed one also
  drops the conversation id. Sources follow as a block.
- Context: the server treats `system` messages as a prompt override that agents
  ignore by default, so ask and chat put a `<context>` block (cwd, first 50 entries
  with `/` on dirs, AGENTS.md or else CLAUDE.md of every dir from the git root down
  to cwd within 12KB, shell history only with `send_last_commands`, no placeholder
  text) before the first user message, and again only when it changes.
  `--no-context` keeps the tools; `--no-tools` drops them.
- Sessions (`internal/session`): created lazily with the first answer; header
  (cwd, server, key, conversation_id), then `message` lines (the sent message, `text`
  = what was typed, sources on the last assistant message) and `state` lines (server,
  key, conversation id) when any of them changes; loading takes the latest. `-c` = latest in cwd, `-r`/`/resume` = filterable
  picker (first message · age · count · key). Resuming shows every exchange again
  under `── resumed · 3h ago · N messages ──`, restores history, the
  conversation id and the last context block; a chat of another key switches to it
  when stored (and no --key/env override), else warns and goes on in a new
  conversation; another server likewise. /new and /key start a new session.
In ask, the approval prompt is a bubbletea program on stderr; it reads keys in raw mode, so Ctrl+C there cancels the run at once.

### Auto-update flow
Modes via `settings.auto_update` ("on" default / "notify" / "off", `config set auto_update`); env kill switch `DOCSGPT_NO_UPDATE_CHECK`.
1. On TTY launches, `updateGate` in root.go decides the mode (skips dev builds, the update/host commands; Homebrew or unwritable installs downgrade on → notify)
2. A detached worker (`update --worker`) refreshes the release cache daily and, in "on" mode, downloads + sha256-verifies the new binary into ~/.docsgpt/staging
3. The next launch validates the staged manifest and swaps it in near-instantly; the old binary is kept in ~/.docsgpt/backup for `update --rollback`
4. Rollback records a skip version so auto-update won't reinstall it; a manual `update` clears the skip
5. Host daemons check every ~12h while idle (10 min boot delay), apply directly, then restart: exec(2) on Unix (same PID), exit(3) on Windows (Task Scheduler RestartOnFailure); all shipped service configs restart only on failure since a revoke exits 0
6. Everything is stamped release-version-only: `update` refuses "dev"/git-describe builds and Homebrew-managed binaries
7. What's new: every check stores the latest release's notes; Apply and ApplyStaged record the installed version, and the next chat on that version shows the notes once (cmd/whatsnew.go)

### bench command
1. Loads a suite dir (default `./bench`): optional `bench.yaml` defaults + any subdir with a `case.yaml`
2. Per case: uploads attachments (waits for the extraction task) or, with `attachments_mode: inline` (v1 only), base64-embeds them as content parts; asks the agent through the case's target (`v1`/`stream`/`answer`/`webhook`), evaluates `expect` assertions
3. Model selection: `model:` (suite/case) or `--model` → `model_id` on stream/answer, `model` on v1; stamped into every result. NOTE: the server currently ignores `model_id` for agent-bound (api_key) requests — honoring it is a docsgpt-cloud change
4. Multi-turn `turns:` — `conversation_id` carried on stream/answer, messages replayed on v1; per-turn `expect` optional, case `expect` grades the last turn; timeout per turn, `limits.max_seconds` whole case; judge sees the transcript
5. Assertions: answer text (contains/regex/…), JSON fields via gjson paths, sources count, tool calls, LLM-as-judge rubric (`judge.model`/`temperature` forwarded, verdicts recorded), latency/TTFT/token limits, `expect.stream` SSE integrity (stream target), `expect.error` negative cases (server error expected; success fails), golden snapshots (`bench record`)
6. Repeat/min_pass for flaky LLMs, `--vs` A/B, `--matrix m1,m2` per-model runs + comparison table (JSON keyed results[model][case]), `--baseline last` regression diff, `--json`/`--junit` for CI; exit codes 0/1/2
7. Cost: `/api/models` fetched once per base URL (tolerant pricing reader) merged with `bench.yaml pricing:`; `cost_usd` per run when usage (v1) and a price for the effective model exist
8. Webhook target sends `{"question": ...}` — the server passes the serialized JSON verbatim as the agent query; no attachments/turns there, approval-gated tools auto-denied
9. Secrets: YAML values support `${VAR}` interpolation (shell env > suite/.env > ./.env, `$$` escape, comment lines exempt, unset var = load error); `--webhook-url` injects the webhook token without YAML
10. Hygiene: `--run-tag`/`run_tag:` → `X-DocsGPT-Bench-Tag: bench:<tag>` + `docsgpt-cli/<ver> bench` User-Agent on target and judge requests (uploads/polls carry the User-Agent)
11. Agent by id: `agent_id:` (suite or case; mutually exclusive with `agent:` at the same level, the case level wins as a whole) or `--agent-id` → stream/answer send `agent_id` in the body + `Authorization: Bearer <PAT>` (scope `chat:run`) instead of `api_key`; attachments upload (and its task polling) and the `/api/models` pricing fetch carry the PAT too (pricing retries anonymously on 401/403). The PAT is only ever sent to the origin the user configured (`--url`, else `DOCSGPT_URL`/config; `spec.SameOrigin`): an `agent_id` case whose YAML `base_url` points elsewhere is a case error, and pricing for such a URL is fetched anonymously. `v1`/`webhook` + `agent_id` is a load/run error; `agent_id` without a token is a case error; api_key runs are byte-for-byte unchanged

### Personal access tokens (account-level commands)
1. A PAT (`dgpt_pat_…`) is created in the web app; the CLI only consumes one (`/api/user/tokens` is closed to tokens). Resolution: `--token` > `DOCSGPT_TOKEN` > `config.json` `token`; base URL: `--url` > `DOCSGPT_URL` > config. Tokens are only ever printed redacted (`config.RedactToken`, first 15 chars + `…`)
2. `login` takes the token from `--token`, piped stdin, or the TTY prompt (see Credentials above), validates it with `GET /api/user/me` and stores it together with the base URL that validated it, whether that came from `--url`, `DOCSGPT_URL` or the config (config stays 0600); when agent keys are stored and the server changes, it asks first (off a terminal: exit 2), as saving a key does for the token. `whoami` prints user, token name, scopes and resource restrictions; `logout --token` removes the stored token
3. `internal/manage` sends `Authorization: Bearer <PAT>`; server failures become `*manage.APIError` — `{success:false,message}`, 401 `invalid_token`, 403 `insufficient_scope` (+ `required_scope`), `resource_not_allowed`, `not_available_to_tokens`
4. `agents plan|apply -f`: files, directories (`*.yaml`/`*.yml`, sorted, not recursive) and `-`; multi-document files are split textually (the server gets each document verbatim); non-`Agent` kinds are rejected before any request. ALL documents are planned first (`POST /api/import_agent/plan {"yaml"}`, scope `agents:write`); if any reference is `missing`/`unavailable` and not covered by `--resolve`, nothing is applied and the exit code is 1. Then `POST /api/import_agent {"yaml","resolution"}` per document, stopping at the first failure. `plan` = `apply --dry-run`
5. `--resolve <kind>:<selector>=<value>` → server `resolution`: `source:<name>=<id>` → `sources[name]`; `tool:<sel>=reuse:<id>|create|skip` and `tool:<sel>.secret.<field>=<v>` → `tools["tool-N"] = {decision, tool_id, secrets}`; `model:<display_name>=<api_key>` → `models[name] = {api_key}`. `source:…=skip` / `model:…=skip` are CLI-side acknowledgements (the server has no such decision; it just leaves the reference off) and are never sent. `<sel>` = `tool-N` or an unambiguous tool name/type; positional keys are refused across several documents; an entry matching nothing is a usage error
6. `sources upload`: multipart `user` (legacy, required by the server), `name`, repeated `file`, with an explicit Content-Length and streamed file bodies. Default `Idempotency-Key` = `docsgpt-cli-upload-` + sha256(name + sorted (basename, file sha256)), so CI retries dedupe; `--wait` polls `/api/task_status` (1s → 10s backoff, 503 = transient, progress on stderr) until SUCCESS / FAILURE / `--timeout`; the `deduplicated` task id sentinel is not polled. `--replace` (needs `--wait`) then deletes the caller's older same-named sources (never the new one, never team-shared, never without a reported `source_id`, and never unless that id is in the current listing: a content revert repeats the Idempotency-Key, and the deduplicated reply then names the earlier, already deleted source; the command fails with exit 1 instead of deleting the only live one): the server resolves an agent's source name to the OLDEST match, so without it agents stay pinned to the first upload; `agents apply` must run after
7. `agents trigger`: payload from `-f <file|->`, validated as JSON (not null) before any request. Target = `--webhook-url` (else `DOCSGPT_WEBHOOK_URL` when no agent id is given; no PAT needed) XOR `<agent-id>` (PAT, scope `agents:keys`, `GET /api/agent_webhook?id=`; the returned token is re-rooted on the configured base URL, since the server builds the URL from its `API_URL`). The webhook POST never carries the PAT (the server denies tokens on that route). `--wait` polls `/api/task_status` anonymously (the server does not require auth there) and falls back to the PAT on 401/403 only when the PAT's base URL has the webhook's origin; SUCCESS whose result is not `status: success` (`quota_exceeded`, idempotency guard) is a failure. The webhook token is never printed (`Webhook.String` redacts; errors pass through `Webhook.Redact`)
8. Exit codes mirror bench: 0 ok, 1 failure/blocked/timeout, 2 usage or validation (`exitError` in cmd/manage.go, mapped in `Execute`). These commands print errors to stderr; destructive ones (`agents/sources delete`, `logout`, `host reset`) ask with `ui.Confirm` via `confirmDestructive`, and refuse without `--yes` off a terminal

### Tool call flow
1. CLI sends `tools` array in request
2. If model returns `finish_reason: "tool_calls"`, CLI shows the call's title (`$ cmd`, `read path`, `write path (+N −M)` with a short diff) and asks: Approve (a), Always allow (l, only when there is a narrow key), Always approve (p: every later call of the session unasked, i.e. AutoApprove), Deny (d), Edit (e, commands only: prefilled input, then asked again); the muted line under the row describes the highlighted choice. Ctrl+C/Esc at the prompt cancels the whole run
3. On approve: executes locally (command output in a live 5-line tail, then `✓ exit 0 · 1.2s` / `✗ …`), sends result back as `role: "tool"` message
4. Model continues with tool results — loop repeats until `finish_reason: "stop"`
5. Security stance (like pi): approval is the only gate, there is no command blocklist. `read_file` asks only for files outside the working directory (symlinks resolved), when the working directory is the home directory or above it (compared as files, so case-insensitive spellings count), or for secret-looking names on the way (`.env*`, `.envrc`, `.netrc`, `*.pem`, `*.key`, `id_*`, `.ssh`, `.aws`, `.docsgpt`, `*_history`, … `secretNames`); devices, FIFOs and directories are refused. "Always allow" lasts for the session: all writes, all reads, or later commands with the same key — the program plus its subcommand word (`git status`, `npm test`), the program alone for plain read-only programs (`ls`, `cat`, `rg`) or option-only calls — and each later command is re-checked. Never offered for shell syntax beyond quotes, `VAR=value` prefixes, programs given as a path, code runners (shells, wrappers, interpreters, `find`, `make`, `tar`, editors, `cmd`/`powershell`/`start`), risky options (`-c`, `-C`, `-e`, `-o`, `-x`, `--exec*`, `--upload-pack`, `--git-dir`, …), risky subcommands (`git config`, `npm exec`, `docker run`), a path argument leaving the cwd (absolute, `~`, `..`), or options before the subcommand other than known value-less ones (`git --no-pager`); the doc comment of `alwaysKey` is the source of truth. Known gap (documented in docs/tools.md): a subcommand key covers its destructive forms (`git branch` → `git branch -D`). Commands run without the terminal, so password prompts fail at once. `--auto-approve` (or "Always approve", or `/approve` in chat) skips every prompt but still prints each title and status. Host mode (no person at the device) keeps its own denylist in `internal/host/invocation.go`

## Config

Single file: `~/.docsgpt/config.json`

```json
{
  "base_url": "https://gptcloud.arc53.com",
  "default_key": "my-agent",
  "keys": { "my-agent": "abc-123-key" },
  "token": "dgpt_pat_…",
  "settings": {
    "send_current_directory": true,
    "send_directory_contents": true,
    "send_project_instructions": true,
    "send_last_commands": false,
    "number_of_last_commands": 3,
    "auto_update": "on"
  }
}
```

`token` is optional (written by `login`, removed by `logout`, omitted when empty); the file is always written as a 0600 temp file and renamed into place (atomic, never readable by others, tightens an older permissive file). Environment overrides: `DOCSGPT_API_KEY`, `DOCSGPT_TOKEN`, `DOCSGPT_URL`. `config get|set <key>` use the setting names `url` (= `base_url`), `default_key` and the `settings` fields; `config show` redacts keys (`config.RedactKey`, first/last 4) and the token.

Settings missing from the file keep their defaults (`Load` decodes over `DefaultConfig`); `send_last_commands` defaults to false for new configs only, since saved configs always carry it. Also under `~/.docsgpt`: `history` (chat prompts) and `sessions/` (saved chats).

Auto-migrates from old `~/.docsgpt-keys.json` + `~/.docsgpt-settings.json` on first run.

## Key dependencies

- `spf13/cobra` — CLI framework
- `charmbracelet/glamour` + `lipgloss` — markdown rendering and styling (our own style, no auto-style query)
- `alecthomas/chroma` — code block highlighting, colored from the palette
- `charmbracelet/bubbletea` (v1) — every interactive prompt, and the full-screen chat
- `charmbracelet/x/ansi`, `muesli/termenv`, `mattn/go-isatty`, `x/term`, `x/sys` — widths and wrapping, color profile, TTY checks, raw mode / echo off
- `atotto/clipboard` — clipboard access
- `rivo/uniseg` — grapheme widths for the chat's mouse selection
- `minio/selfupdate` — atomic binary replacement for the update command
- `golang.org/x/mod/semver` — version comparison
- `gopkg.in/yaml.v3` — bench suite/case files
- `tidwall/gjson` — JSON path assertions in bench

## Build & run

```bash
go build -o /tmp/dg ./cmd/docsgpt-cli && /tmp/dg --help   # outside the repo
make build                                                # ./docsgpt-cli (gitignored), version stamped
```

## Notes

- Module path is `github.com/arc53/DocsGPT-cli` (mixed case, matching the repo; the Go proxy escapes it as `!docs!g!p!t-cli`, which users never type). `go.work` is committed and spans `.` and `./sdk`
- `cmd.Version` is stamped by ldflags for release and `make build`; `resolveVersion` in root.go recovers it from `debug.ReadBuildInfo` for `go install` builds, which carry no ldflags and would otherwise report "dev" and disable their own update checks
- Releases: `.github/workflows/release.yml` (dispatch, or a hand-pushed `v*` tag) → GoReleaser builds linux/darwin/windows (amd64+arm64) archives + checksums.txt with stable asset names, attaches `deployment/install.sh`/`install.ps1`, and commits a Homebrew **cask** to `arc53/homebrew-DocsGPT-cli` with `HOMEBREW_TAP_TOKEN` (`skip_upload: auto` keeps prereleases out of brew; the job only runs on `arc53/DocsGPT-cli`). Cut from Actions → Release → Run workflow (a `cli`/`sdk` bump input; one run tags the sdk, pushes the `go.mod` pin bump, then tags and releases the CLI in that order) — there is no local release target; see `RELEASING.md`
- Install script: `docs.ac/install-cli` redirects to `releases/latest/download/install.sh`, so the live installer is whatever the newest release carries — a fix lands only on the next tag. Both installers verify the archive against `checksums.txt`, then hand off to `docsgpt-cli install`, which owns the PATH logic for every platform
- SSE parsed with stdlib bufio (no external SSE lib), separately in sdk/client.go, bench/target and host/transport
- Shell history (opt-in context): zsh, bash, fish
- Cross-platform: Unix + Windows
