# DocsGPT-CLI

DocsGPT-cli is a command-line interface (CLI) tool that allows you to interact with [DocsGPT](https://github.com/arc53/DocsGPT). It enables you to ask questions, configure settings, and manage DocsGPT API keys directly from your terminal.

---

## Installation

You can install DocsGPT-cli in three ways:

### 1. Download the Binary

Download the latest binary from the [Releases page](https://github.com/arc53/DocsGPT-cli/releases). You can run it as is or use the `install` command to add the binary to your system's `PATH`:

```bash
./docsgpt-cli
./docsgpt-cli install
```

### 2. Compile from Source

If you want to make adjustments or compile the binary yourself, clone the repository and compile it:

```bash
git clone https://github.com/arc53/docsgpt-cli.git
cd docsgpt-cli
make build
```

After compiling, follow the same steps as for the binary:

### 3. Install via Homebrew

If you prefer using Homebrew, you can install DocsGPT-cli with the following commands:

```bash
brew tap arc53/docsgpt-cli
brew install docsgpt-cli
```

---

## Usage

Once installed, you can start using `docsgpt-cli` by running the following commands:

```bash
docsgpt-cli [flags]
docsgpt-cli [command]
```

### Available Commands:

- `ask` — Ask a question to DocsGPT
- `chat` — Start an interactive chat session
- `config` — Manage CLI configuration (base URL, theme, banner, update check)
- `help` — Help about any command
- `install` — Install docsgpt-cli to your system's `PATH`
- `keys` — Manage DocsGPT API keys (add, set default, delete)
- `update` — Update docsgpt-cli to the latest release

### Flags:

- `-h, --help` — Help for docsgpt-cli
- `-v, --version` — Version for docsgpt-cli

You can use `docsgpt-cli [command] --help` to get more information about each command.

---

## Updating

`docsgpt-cli` keeps itself up to date. It checks GitHub for a new [release](https://github.com/arc53/DocsGPT-cli/releases) in the background at most once a day, downloads and verifies it, and installs it the next time you run a command. Control this behavior with:

```bash
docsgpt-cli config set-auto-update on      # download and install automatically (default)
docsgpt-cli config set-auto-update notify  # only print a notice when a release is available
docsgpt-cli config set-auto-update off     # never check
```

Manual controls:

```bash
docsgpt-cli update            # check, confirm, and install now
docsgpt-cli update --check    # only check for a new version
docsgpt-cli update --yes      # skip the confirmation prompt
docsgpt-cli update --rollback # restore the binary from before the last update
```

A rollback also tells auto-update to skip the version you rolled back from until you run `docsgpt-cli update` yourself.

Setting the `DOCSGPT_NO_UPDATE_CHECK` environment variable disables everything update-related. Homebrew installs are never touched — update those with `brew upgrade docsgpt-cli`. Long-running hosts (`docsgpt-cli host`) check occasionally while idle, install the new release, and restart themselves into it.

Here’s the updated section with the paragraph about the prompt:

---

## Customizing the Prompt

We recommend changing the default DocsGPT prompt to make your interactions more efficient. By using a more concise prompt, you can get faster and more focused responses. For example, you can set the prompt to:

```
You are a embedded cli assistant docsgpt. You help users from terminal. Keep your answers very short. Just answer with a command if applicable.
```

---

## Code Of Conduct

We as members, contributors, and leaders, pledge to make participation in our community a harassment-free experience for everyone, regardless of age, body size, visible or invisible disability, ethnicity, sex characteristics, gender identity and expression, level of experience, education, socio-economic status, nationality, personal appearance, race, religion, or sexual identity and orientation. Please refer to the [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) file for more information about contributing.
