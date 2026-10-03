# docsgpt-cli documentation

`docsgpt-cli` chats with your DocsGPT agents from the terminal, answers
one-off questions in pipelines, and manages agents and sources as code.

New here? Start with the [Quickstart](quickstart.md).

## Use it

- [Install and update](install.md): install script, Homebrew, `go install`,
  auto-update and rollback
- [Quickstart](quickstart.md): sign in, chat, ask once, pipe input, attach files
- [Chat](chat.md): editor keys, attachments, slash commands, `!cmd`, sessions, context
- [Tools and approval](tools.md): what the agent can do on your machine, and
  what you are asked
- [Configuration](configuration.md): config file, credentials, environment
  variables, settings

## Automate it

- [Agents as code](agents-as-code.md): personal access tokens, export, plan,
  apply, trigger
- [Sources](sources.md): upload, wait for ingestion, replace old copies
- [Bench](bench.md): test agents' answers, compare agents and models
- [CI](ci.md): GitHub Actions for all of the above
- [Go SDK](../sdk/README.md): the chat client the CLI is built on

## Run it as a device

- [Host mode](host.md): pair a machine and let agents run commands on it

## Reference

Every command has `--help` with its flags and examples:

```bash
docsgpt-cli --help
docsgpt-cli agents apply --help
```

Examples live in [`examples/`](../examples): an [agent
definition](../examples/agents), a [bench suite](../examples/bench) and a [CI
workflow](../examples/ci/github-actions.yml).
