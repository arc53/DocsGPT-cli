# Quickstart

`docsgpt-cli` talks to a DocsGPT agent with the agent's API key. You need
DocsGPT ([cloud](https://gptcloud.arc53.com) or your own server) and an agent.

## 1. Install

```bash
curl -fsSL https://docs.ac/install-cli | bash
docsgpt-cli --version
```

Other platforms and methods: [Install and update](install.md).

## 2. Sign in

In DocsGPT, open the agent and copy its key from **Agent settings → API key**.
Then:

```bash
docsgpt-cli login
```

Paste the key. The CLI checks it with the server, offers the agent's name as
the key's name, and saves it (the first key becomes the default). For your own
server, add `--url`:

```bash
docsgpt-cli login --url http://localhost:7091
```

Skipping this step is fine: the first chat asks for a key. Without a terminal,
pipe it in:

```bash
echo "$AGENT_KEY" | docsgpt-cli login --name support
```

Add more keys the same way and switch between them with `docsgpt-cli login`, or
`/key` in a chat. See [Configuration](configuration.md) for credentials and
settings.

## 3. Chat

```bash
cd ~/src/my-project
docsgpt-cli
```

Type a message and press Enter. The answer streams in as rendered markdown,
followed by the sources it drew on. Type `/` for the commands. When the agent
wants to run a command or touch a file, you are asked first.

Leave with Ctrl+D. Come back to the same conversation with:

```bash
docsgpt-cli -c
```

More in [Chat](chat.md) and [Tools and approval](tools.md).

## 4. Ask once

A question as an argument gets one answer, and the command exits:

```bash
docsgpt-cli "how do I rotate the API key?"
```

Anything piped in is sent along with the question, or is the question when
there is none:

```bash
git diff | docsgpt-cli "review this"
tail -n 50 app.log | docsgpt-cli "why does this fail?"
docsgpt-cli < question.txt
```

- When stdout is not a terminal, only the answer is written, as plain text with
  terminal control sequences removed: `docsgpt-cli "summarize the README" > summary.md`.
- Unless stdin is a terminal or a device such as `/dev/null`, it is read to
  its end, so a slow producer (`make 2>&1 | docsgpt-cli "why?"`) is waited
  for. Input over 1 MB is cut, with a note on stderr.
- Where stdin stays open without input, pass `--no-stdin` (or redirect it from
  `/dev/null`): `ssh host docsgpt-cli …` without `-t`, CI runners that keep
  stdin open, and `while read` loops, whose input it would swallow. On a
  terminal, a stdin silent for a second shows
  `waiting for piped input… (--no-stdin to skip)`.
- On a terminal, the first `bash`/`sh` code block of the answer is copied to
  your clipboard.
- The agent's tools are offered only when someone can approve them: stdin is a
  terminal, or you pass `--auto-approve`.
- A single word that looks like a mistyped command (`docsgpt-cli agnets`) is
  refused with a hint. `docsgpt-cli -- <words>` always sends the words as a
  question.

## Tip: a terminal prompt

A short prompt makes answers faster and more to the point. In DocsGPT, give the
agent a prompt such as:

```
You are an embedded CLI assistant, DocsGPT. You help users from the terminal.
Keep your answers very short. Just answer with a command if applicable.
```

## Next

- [Chat](chat.md): editor keys, slash commands, sessions, context
- [Agents as code](agents-as-code.md) and [Sources](sources.md), with a
  personal access token
- [CI](ci.md): deploy and test agents from GitHub Actions
