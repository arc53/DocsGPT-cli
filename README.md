# docsgpt-cli

Chat with your [DocsGPT](https://github.com/arc53/DocsGPT) agents from the
terminal, pipe questions through them, and manage agents and sources as code.

## Install

```bash
curl -fsSL https://docs.ac/install-cli | bash
```

The script downloads the release for your platform, checks it against the
published checksums and puts it on your `PATH`. Other ways:

- **Windows** (PowerShell): `irm https://docs.ac/install-cli.ps1 | iex`
- **Homebrew** (macOS): `brew tap arc53/docsgpt-cli && brew install --cask docsgpt-cli`
- **Go**: `go install github.com/arc53/DocsGPT-cli/cmd/docsgpt-cli@latest`
- **Binary**: an archive from [Releases](https://github.com/arc53/DocsGPT-cli/releases), then `./docsgpt-cli install`
- **Source**: `git clone https://github.com/arc53/DocsGPT-cli && cd DocsGPT-cli && make build`

It keeps itself up to date. See [Install and update](docs/install.md).

## Quickstart

Copy an agent's API key from DocsGPT (**Agent settings → API key**), then:

```bash
docsgpt-cli login                            # paste the key; it is checked and saved
docsgpt-cli                                  # chat
docsgpt-cli "how do I rotate the API key?"   # ask once and exit
git diff | docsgpt-cli "review this"         # send piped input along
docsgpt-cli -c                               # continue the latest chat here
```

The first chat asks for a key if you skip `login`. When stdout is not a
terminal, only the answer is written, as plain text:

```bash
tail -n 50 app.log | docsgpt-cli "why does this fail?" > diagnosis.md
```

More in the [quickstart](docs/quickstart.md).

## What you get

- **A full-screen chat.** It scrolls with the wheel and the keys while answers
  stream in, and leaves the conversation in your scrollback when you quit.
  Multi-line input, prompt history, `$EDITOR`, `/` commands and `!cmd` to share
  a command's output. [Chat](docs/chat.md)
- **Sessions.** Every chat is saved per directory; `-c` continues the latest,
  `-r` picks one. [Sessions](docs/chat.md#sessions)
- **Local tools.** The agent can run commands and read and write files, after
  you approve each one. [Tools and approval](docs/tools.md)
- **Sources shown.** Answers end with the documents they drew on.
- **Agents as code.** Export agents as YAML, review a plan, apply it from CI.
  [Agents as code](docs/agents-as-code.md)
- **Sources from CI.** Upload docs, wait for ingestion, replace the old copy.
  [Sources](docs/sources.md)
- **Bench.** Assert on your agents' answers, compare agents and models.
  [Bench](docs/bench.md)
- **Host mode.** Pair a machine with your DocsGPT account and let agents run
  tools on it. [Host mode](docs/host.md)
- **Auto-update.** New releases are downloaded, verified and installed in the
  background. [Updating](docs/install.md#updating)
- **A Go SDK.** The chat client the CLI is built on. [sdk](sdk/README.md)

## Documentation

- [Docs index](docs/README.md)
- [Configuration](docs/configuration.md): config file, credentials, environment
  variables, settings
- [CI](docs/ci.md): GitHub Actions for sources, agents, bench and webhooks

Run `docsgpt-cli <command> --help` for every flag.

## Contributing

Read [AGENTS.md](AGENTS.md) for the development rules and
[RELEASING.md](RELEASING.md) for how releases are cut. Please follow the
[Code of Conduct](CODE_OF_CONDUCT.md).

## License

MIT
