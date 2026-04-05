# Operations

Primary workflow:

```bash
task slack-keychain-store
task run    # builds and starts rook, loading Slack tokens from Keychain
task test
task lint
```

If token storage fails with `User interaction is not allowed`, unlock the login keychain and retry:

```bash
security unlock-keychain ~/Library/Keychains/login.keychain-db
task slack-keychain-store
```

The Slack bot exposes runtime commands in chat:

- `help`
- `ping`
- `status`
- `memory`
- `model`
- `reload`
- `remind`

Autonomous behavior is configured in `config/rook.toml`. For the built-in weeknote job, enable:

- `autonomy.enabled = true`
- `autonomy.weeknotes_enabled = true`
- `autonomy.weeknotes_channel = "C..."`

The weeknote scheduler uses the service timezone and posts once per week after Friday 10:00 local time.

launchd assets live in `launchd/` and `scripts/install-launchd.sh`.
The launchd service starts `scripts/run-rook.sh`, which reads Slack tokens from macOS Keychain and exports them as `ROOK_SLACK_*` environment variables before executing the Go binary.

Install and load the agent:

```bash
task launchd-install
launchctl list | grep io.rook.agent
```
