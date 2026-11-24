# Independent review packet: #3

Act as an independent senior engineer reviewing the pull request for issue #3. Do not rely on the implementer's summary. Inspect the exact base/head commits and full diff.

Verify:

1. `help`, `/help`, and `!help` are handled deterministically in Discord, Slack, and Telegram; Telegram accepts `/help@<own-bot>` but not another bot's address, and Discord registers native `/help`.
2. Help responses do not enter the message bus, invoke the model, mutate conversation history, add reactions, or start typing indicators.
3. Native Discord help enforces the channel allowlist; normal messages, messages with attachments, mentions, replies, Slack threads/app mentions, and non-help slash commands retain existing behavior.
4. The response lists the supported commands and links to the documentation site.
5. Tests exercise the native Discord registration/dispatch path and no-bus behavior, plus command matching, Telegram bot addressing, negative cases, and the documented command payload.
6. The diff contains no unrelated changes, generated debris, secrets, or stale implementation paths.

Run focused channel tests, race tests, the broad test suite, `go vet`, `go build`, `git diff --check`, and inspect the final diff. Return a verdict, findings by severity with file/line references, commands rerun, unverified claims, and minimal remediation before merge. Live Discord/Slack/Telegram accounts are not required for this review; live delivery remains unverified. Do not merge or push fixes.

Final QA additions: Slack `handleMessageEvent` and `handleSlashCommand` tests assert exact outbound channel/thread targets, zero inbound work, and denied-user zero sends; Telegram tests cover own-bot group addressing with exact chat ID and denied-sender zero sends/no inbound work. Positive inbound assertions use bounded contexts to avoid hanging on regressions.
