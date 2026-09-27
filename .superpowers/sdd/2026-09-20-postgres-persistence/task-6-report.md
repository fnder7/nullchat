# Task 6 implementation and verification report

Date: 2026-09-26
Worktree: `/home/aidar/projects/nullchat/.worktrees/postgres-persistence`
Base: `fa7a29abc940aab42eae7acb581795f6fff9a463`
Commit message: `feat: add transactional PostgreSQL chat operations`

## Implementation

- Completed PostgreSQL `Send`, `End`, `Expire`, `Ready`, and `Close`, with the `chat.Application` compile assertion.
- Send resolves the session, locks room then session, and rechecks room/session expiry and revocation against fresh `clock_timestamp()` after locking. Normalized client-message retries return the original message without publication; changed text conflicts. New messages enforce 500 messages and a 200 ms per-session interval, allocate the room's next sequence, update counters and send time transactionally, and publish only after commit.
- Preserved Task 5's room-lock -> handoff-mutex -> commit/publication ordering. Tests cover 50 Subscribe/Send races and controlled subscriber-first and committed-writer-first boundaries, including snapshot-first delivery and no duplicate live event.
- Shared `closeRoomTx` conditionally transitions open rooms, records `closed_at`, revokes every room session, and consumes outstanding invites. End authenticates and publishes `room_closed` after commit. Expire drains batches of 100 with `FOR UPDATE SKIP LOCKED`, publishes `room_expired` after each committed batch, skips held locks, and safely retries.
- Ready pings the pool. Task 6 database failures map to `ErrDatabaseUnavailable`; caller cancellation/deadlines remain their context sentinel. Driver-internal cancellation/timeout details are sanitized when the caller context remains live.
- Added `Hub.CloseAll` and its test under the existing ledger ruling. Service Close serializes with snapshot enqueue and closes only subscriptions; it preserves rooms, session/invite access, and the caller-owned pool.
- Added 15 PostgreSQL tests covering normalization, concurrent duplicate retries, conflicts, no-gap handoff, concurrent sequence ordering, limits/rate, expiry and session changes during lock waits, end/expiry revocation, skipped locks and multiple expiry batches, commit rollback/no publication, database/context errors, and non-destructive Close. Added one hub shutdown test.

## Exact checks

All Go commands below used `GOMODCACHE=/tmp/nullchat-task4-gomod GOCACHE=/tmp/nullchat-task4-gocache`. Live database commands additionally used `TEST_DATABASE_URL=postgres://nullchat:nullchat@127.0.0.1:55436/nullchat_test?sslmode=disable`.

1. Baseline `go test ./...` — exit 0; PostgreSQL-backed cases skipped because TEST_DATABASE_URL was initially unset.
2. RED: `go test -tags=integration ./internal/storage/postgres -run 'TestDuplicateMessageIsStoredOnce|TestSubscribeAndSendHaveNoGap|TestEndRevokes|TestSendRechecksExpiryAfterRoomLock' -v` — exit 1, expected missing Send/End methods before implementation.
3. RED: `go test ./internal/realtime -run TestHubCloseAll -v` — exit 1, expected missing Hub.CloseAll before implementation.
4. GREEN: `go test ./internal/realtime -v` — exit 0, all five tests passed.
5. Live focused GREEN: `go test -tags=integration ./internal/storage/postgres -run 'TestDuplicateMessageIsStoredOnce|TestSubscribeAndSendHaveNoGap|TestSubscribeSendControlledHandoff|TestEndRevokes|TestSendRechecksExpiryAfterRoomLock|TestSendLimitsAndRate|TestConcurrentSendSequences|TestExpiryRevokes|TestExpireSkips|TestOperationsCommit|TestReadyAndOperation|TestClosePreserves' -count=1 -v` — exit 0, all 12 selected top-level tests passed, no skips; package 3.393s.
6. Error-boundary regression: `go test ./internal/storage/postgres -run TestDatabaseErrorPreservesOnlyCallerCancellation -count=1 -v` — first exit 1 because a wrapped driver timeout escaped the application boundary; after correction exit 0. Caller context now determines cancellation propagation.
7. Live integration/race: `go test -tags=integration -race ./internal/storage/postgres -count=1 -v` — exit 0, all 38 top-level tests passed, no skips or race reports; package 10.223s.
8. Live full suite: `go test ./... -count=1` — exit 0, every package passed; PostgreSQL 8.317s. cmd/migrate, cmd/server, and web have no test files.
9. Live full integration/race suite: `go test -tags=integration -race ./... -count=1` — exit 0, every package passed, no race reports; PostgreSQL 10.351s.
10. `go vet ./...` — exit 0, no output.
11. `gofmt -w` applied to `internal/storage/postgres/service.go`, `internal/storage/postgres/send_end_test.go`, `internal/realtime/hub.go`, and `internal/realtime/hub_test.go`; subsequent `gofmt -l` on those four files produced no output.
12. `git diff --check` — exit 0, no output. Staged whitespace checks are required immediately before the commit.

## Environment and review

- TEST_DATABASE_URL was initially unset. Docker access was denied in the sandbox; approved elevated execution started the disposable `nullchat-task6-postgres` PostgreSQL 17 Alpine container on loopback port 55436. All live checks used this dedicated database with approved network access; no existing database was modified. No live database verification limitation remains.
- Go caches were reused under `/tmp` to stay within writable paths. Committing requires approved access to the worktree's shared Git metadata.
- Self-review used the code-review checklist against Task 6, the design, Task 5's locking/handoff implementation, and the ledger ruling. No outstanding Task 6 findings. No independent reviewer tool is available; this is author self-review, not independent review.
- The execution/TDD skills guided test-first implementation and verification. The explicit user scope overrides skill instructions to edit the ledger, advance to later tasks, or delete the SDD workspace. The plan, ledger, Task 5 idempotency code, and Task 7 runtime wiring are unchanged.
- This report is explicitly included with the four scoped Go files in the requested commit, despite the surrounding SDD directory being ignored, to satisfy the instruction to commit all changes.

## Fix round — NUL input validation

Reviewer finding: Important. `Send` began a PostgreSQL transaction before normalizing input. PostgreSQL text parameters reject embedded NUL bytes, so a NUL in `client_message_id` or message text could enter the database error path and be reported as `database_unavailable` instead of a client input error.

Fix: `Send` now rejects NUL bytes in both fields and runs the existing `chat.NormalizeMessage` before any pool access. Existing trimming, UTF-8, client-ID, empty-body, and 4,000-byte checks remain unchanged. The focused test uses a nil pool to prove both cases return `chat.ErrInput` before SQL can execute.

TDD evidence:

1. RED: `go test ./internal/storage/postgres -run TestSendRejectsNULInputBeforeDatabaseAccess -count=1 -v` — exit 1 with the expected nil-pool panic, proving validation occurred too late.
2. GREEN: the same focused command after the fix — exit 0; both `client_id` and `message` subtests passed.
3. Focused live checks — exit 0: duplicate messages, both no-gap handoff orderings, 50-run Subscribe/Send boundary loop, limits/rate, NUL cases, and Hub.CloseAll.
4. `go test -tags=integration -race ./internal/storage/postgres -count=1` — exit 0; all PostgreSQL tests passed with no races.
5. `go test ./... -count=1` — sandboxed attempt was blocked by httptest IPv6 listener permissions; exact rerun with approved local network access exited 0, all packages passed.
6. `go test -tags=integration -race ./... -count=1` — exit 0, all packages passed with no race reports.
7. `go vet ./...` — exit 0, no output.
8. `gofmt -l internal/storage/postgres/service.go internal/storage/postgres/send_end_test.go` — no output; `git diff --check` — exit 0.

The no-gap test already exercises the publication/registration boundary in both directions through `TestSubscribeSendControlledHandoff`; it was rerun in this fix round and passed. The report, implementation, and focused tests are committed together in the fix-round commit.

## Fix round 2 — context precedence in Send

Reviewer finding: Important. The NUL/input validation added in the prior fix ran before checking the caller context, so a canceled request containing invalid input returned `invalid_input` instead of `context.Canceled` (and likewise could mask `context.DeadlineExceeded`).

Fix: `Send` now checks `ctx.Err()` as its first operation. Canceled or expired contexts return their context sentinel without validating input or accessing PostgreSQL. For live contexts, NUL rejection and the existing normalization/limits remain before `pool.Begin`.

TDD evidence:

1. RED: `go test ./internal/storage/postgres -run TestSendCanceledContextPrecedesInvalidInput -count=1 -v` — exit 1 with `invalid_input`, proving the precedence regression.
2. GREEN: the same focused command after the fix — exit 0; canceled and deadline contexts returned the expected sentinels.
3. Focused live checks: `TestSendCanceledContextPrecedesInvalidInput`, `TestSendRejectsNULInputBeforeDatabaseAccess`, duplicate send, both controlled no-gap handoff orderings, and the 50-iteration no-gap loop — exit 0.
4. `go test -tags=integration -race ./... -count=1` — exit 0, all packages passed with no race reports.
5. `go test ./... -count=1` — exit 0, all packages passed.
6. `go vet ./...` — exit 0, no output.
7. `gofmt -l internal/storage/postgres/service.go internal/storage/postgres/send_end_test.go` — no output; `git diff --check` — exit 0.

The plan, ledger, and Task 7 runtime wiring remain unchanged.
