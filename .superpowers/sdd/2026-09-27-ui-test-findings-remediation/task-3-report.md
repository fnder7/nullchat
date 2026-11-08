# Task 3 Report: Clear the Stale Database Banner on Successful Message Retry

## Change

The manual retry click handler now captures the boolean returned by `transmit(entry)` and clears `chat-error` only when that value is true. A false result leaves existing feedback visible. The same `entry` continues to be passed to `transmit`, preserving its `client_message_id`; no acknowledgement or later WebSocket error handling was changed.

Added `TestFrontendSuccessfulManualRetryClearsStaleError` to isolate the messages click handler and assert the success-only clear behavior.

## TDD evidence

### RED

Command: `GOCACHE=/tmp/nullchat-task3-gocache go test ./web -run TestFrontendSuccessfulManualRetryClearsStaleError -v`

Result: failed as expected with `manual retry handler does not capture the transmit result`. This confirms the test detects the prior implementation.

The initial run without a temporary cache did not reach compilation because the configured Go build cache was read-only. Re-running with the cache under `/tmp` produced the expected RED result.

### GREEN

Commands:

- `node --check web/app.js` — passed.
- `GOCACHE=/tmp/nullchat-task3-gocache go test ./web` — passed (`ok nullchat/web`). This includes the focused regression test and existing web tests.
- `git diff --check` — passed.

## Self-review

The handler only calls `showError('chat-error')` under `if (sent)`, so synchronous failure does not clear feedback. It does not generate or replace a message ID. The WebSocket error event path is untouched and can still present later feedback.

## Commit

`fix: clear stale error after message retry`
