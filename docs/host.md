# Host mode

Host mode turns a machine (a server, a Raspberry Pi, your laptop) into a device
that your DocsGPT agents can run shell commands on. `docsgpt-cli host` runs as
a long-lived daemon paired to your DocsGPT account; it polls the server for
work, runs the commands and streams their output back.

## Pair and run

1. In DocsGPT, open **Settings → Devices → Pair a device**, choose a name,
   description and approval mode, and generate a code.
2. On the machine:

   ```bash
   docsgpt-cli host pair                                  # asks for the XXXX-XXXX code
   docsgpt-cli host pair ABCD-1234 --url https://docsgpt.example.com   # your own server
   ```

   On a terminal it then offers to start the daemon now or install it as a
   service. Off a terminal (`echo CODE | docsgpt-cli host pair`), it just pairs.
3. Start the daemon in the foreground:

   ```bash
   docsgpt-cli host
   ```

The device shows as online in **Settings → Devices**.

## Run as a service

```bash
docsgpt-cli host install-service      # start at boot or logon, restart on failure
docsgpt-cli host uninstall-service
```

| Platform | As a user | As root / with `--system` |
|---|---|---|
| Linux (systemd) | `~/.config/systemd/user/docsgpt-cli-host.service` | `/etc/systemd/system/docsgpt-cli-host.service` |
| macOS (launchd) | `~/Library/LaunchAgents/com.arc53.docsgpt-cli-host.plist` | `/Library/LaunchDaemons/com.arc53.docsgpt-cli-host.plist` |
| Windows (Task Scheduler) | Task `docsgpt-cli-host`, at logon, no admin rights | – |

A system service runs as `--user <name>`, else `$SUDO_USER`, else root.

## Approval

Approval is set per device in the web app, under **Settings → Devices**:

| Mode | Effect |
|---|---|
| Ask | You approve every command in DocsGPT before it runs |
| Full access | Commands run without asking |

Nobody is at the machine to approve, so the daemon follows the server's
decision. In every mode it refuses a short safety denylist (`rm -rf /`, `mkfs`,
`dd if=`, writes to raw disks, `shutdown`, `reboot`, a fork bomb). That list is
a floor, not a sandbox: a command runs with the daemon user's permissions, so
run the host as a dedicated user, or in a container or VM, and give it only
what agents need. See [Tools and approval](tools.md#security).

## Manage

```bash
docsgpt-cli host status   # device, host, online state, approval mode, server
docsgpt-cli host revoke   # revoke on the server and clear local state
docsgpt-cli host reset    # clear local state only (the device stays active on the server)
```

`reset` asks first, and refuses without `--yes` when there is no terminal.

Revoking a device from the web app stops the daemon; it exits `0`, so a service
manager does not restart it. Pair again to reconnect.

## Files and options

| Path | What |
|---|---|
| `~/.docsgpt/host.yml` | Device id, server, poll interval |
| `~/.docsgpt/host.key` | The machine's Ed25519 key |
| `~/.docsgpt/host.log` | The log, for the Windows task (`--service`) |

`--poll-interval <d>` overrides the poll interval (default `10s`).

## Updates

A host checks for a new release about every 12 hours while idle (the first
check 10 minutes after start), installs it and restarts itself into it: in
place on Linux and macOS, through the scheduled task's restart on Windows. It
follows `auto_update` like any other install ([Updating](install.md#updating)).
