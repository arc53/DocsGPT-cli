# Agents as code

`docsgpt-cli agents` applies agent definitions from YAML, so agents can live in
a repository and be deployed from CI with a personal access token.

```bash
export DOCSGPT_TOKEN=dgpt_pat_...          # scopes: agents:write, sources:write
export DOCSGPT_URL=https://docsgpt.example.com

docsgpt-cli whoami                          # confirm the token and its scopes
docsgpt-cli sources upload docs/*.md --name "Product docs" --wait
docsgpt-cli agents plan  -f examples/agents/   # nothing is written
docsgpt-cli agents apply -f examples/agents/
```

`plan` and `apply` print, per document, whether the agent is created or
updated and how every reference (sources, tools, prompt, models) resolves.
When a reference is `MISSING` or `UNAVAILABLE`, `apply` exits 1 **without
applying anything** until it is settled with `--resolve`:

| `--resolve` | Effect |
| --- | --- |
| `source:<name>=<source-id>` | attach this existing source for the named spec source |
| `source:<name>=skip` | accept that the source stays unattached |
| `tool:<sel>=reuse:<tool-id>` | link one of your existing tools |
| `tool:<sel>=create` | create a new tool even if a (type, name) match exists |
| `tool:<sel>=skip` | leave the tool off the agent |
| `tool:<sel>.secret.<field>=<value>` | secret used when the tool is created |
| `model:<display-name>=<api-key>` | API key used to create a custom model |
| `model:<id-or-name>=skip` | accept that the model is dropped |

`<sel>` is the plan key (`tool-0`, `tool-1`, … — the position in `spec.tools`)
or, when unambiguous, the tool's name or type. Positional keys are rejected
when several documents are applied at once.

Start from a real agent with `docsgpt-cli agents export <id> -o my.agent.yaml`.
See [`../ci/github-actions.yml`](../ci/github-actions.yml) for a complete
workflow.
