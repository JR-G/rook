# Operations

Primary workflow:

```bash
task slack-keychain-store
task build
task run
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

launchd assets live in `launchd/` and `scripts/install-launchd.sh`.
The launchd service starts `scripts/run-rook.sh`, which reads Slack tokens from macOS Keychain and exports them as `ROOK_SLACK_*` environment variables before executing the Go binary.

Install and load the agent:

```bash
task build
./scripts/install-launchd.sh
launchctl list | grep io.rook.agent
```
