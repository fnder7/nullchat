# PostgreSQL persistence verification — 2026-09-26

Task 8 was verified in the worktree `.worktrees/postgres-persistence`, a separate Compose project `nullchat-task8`, and a fresh named volume `nullchat-task8_postgres_data`. The application DB is `nullchat`, the test DB is `nullchat_test`; ports on loopback only: 5432 and 8080. A random development key was generated in the environment; its value was not written to the repository or to this report. The output of `docker compose config` was redirected to `/dev/null` so as not to reveal the substituted secret.

Environment: Linux amd64, host Go `1.27.1-X:nodwarf5`, Docker `29.8.0`, Compose `v5.5.1`. The `build` target actually built uses Go `1.26.8`; PostgreSQL is `17.11` from `postgres:17-alpine`. The project's Go floor is 1.25. A C toolchain is installed in the Alpine builder for race; the final scratch image contains `/nullchat` and `/migrate` and runs as `65532:65532`. Both binaries are still built with `CGO_ENABLED=0`, `-trimpath`, `-ldflags="-s -w"`.

## Final whole-branch fix round

The final verification was done in the same worktree from base `693e9fb`. Host Docker is `29.8.1`, Compose is `v5.5.1`. Added: cleanup of expired `idempotency_requests`, JSON `server_shutdown` before close `1001`, a stable `connection_limit` before and after the upgrade race, CA roots in the scratch image, rejection of a title with NUL, and the migration `0002_invites_room_idx.sql`. The plan, `docs/PLAN.md` and the SDD ledger were not changed.

RED→GREEN was confirmed separately:

- `TestExpireDeletesExpiredIdempotencyAndAllowsKeyReuse` first kept one expired row, then passed and confirmed that replay is unavailable before cleanup, that the ciphertext is deleted, and that a new request with the same key works after cleanup.
- `TestExpireCleanupPreservesReplayLockOrder` reproduced the deadlock of the old room-lock → idempotency-delete variant; a separate atomic cleanup statement removed the cycle.
- `TestShutdownCancelsBlockedSubscribeAndWaitsForHandler` and the process-restart test now receive JSON `server_shutdown`, then close `1001`; the pre-upgrade limit returns HTTP `429 connection_limit`, the post-upgrade race returns a JSON error and a retryable close `1013`.
- `TestNormalizeTitleRejectsNUL` and the migration test failed before the fixes; after the change the migration ledger contains versions `1,2`, and `invites_room_idx` exists.
- Before the Dockerfile fix, `docker cp` reported that `/etc/ssl/certs/ca-certificates.crt` was missing; after the rebuild the bundle was extracted from the final Compose image, and `test -s` finished with exit 0.

Final commands actually run:

| Command / check | Result |
|---|---|
| `TEST_DATABASE_URL=... go test -race ./...` | PASS; PostgreSQL package `11.502s`, no race reports |
| `go vet ./...` | exit 0 |
| `go build ./cmd/server ./cmd/migrate` | exit 0 |
| `node --check web/app.js` | exit 0 |
| `docker compose config --quiet` | exit 0 |
| `docker build -t nullchat:final-review .` | exit 0; scratch image built with the CA bundle |
| `docker compose up --build --abort-on-container-exit migrate` | migration job exit 0 |
| `docker compose up --build -d postgres nullchat` | PostgreSQL healthy, migrate exit 0, API started |
| `curl --fail .../healthz` and `curl --fail .../readyz` | HTTP 200: `ok` and `ready` |
| `docker compose run --rm test` | PASS; PostgreSQL integration/race suite `17.256s` |
| SQL check of the migration ledger/index | `versions=1,2`, `invite_index=true` |
| Extracting the CA bundle from `postgres-persistence-app:local` | bundle exists and is not empty |

Full cleanup of rooms/history, WAL/backup retention, outbox/replay, paging and multi-service remain deferred. Only expired idempotency envelopes are physically deleted; no other retention promises were extended.

## Task 8 checks performed

All successful host Go commands used writable caches:

```sh
export GOMODCACHE=/tmp/nullchat-task4-gomod
export GOCACHE=/tmp/nullchat-task4-gocache
```

| Command / check | Actual result |
|---|---|
| `go test -race ./...` | PASS after allowing local sockets; without TEST_DATABASE_URL the DB cases are skipped |
| `TEST_DATABASE_URL='postgres://nullchat:nullchat@127.0.0.1:5432/nullchat_test?sslmode=disable' go test -race ./... -count=1` | PASS, all packages; PostgreSQL `11.421s`, no race reports and no skipped DB cases |
| `go vet ./...` | exit 0, no output |
| `go build ./cmd/server ./cmd/migrate` | exit 0, no output |
| `node --check web/app.js` | exit 0, no output |
| `docker compose config` (with the key, output hidden) | exit 0 |
| `env -u IDEMPOTENCY_ENCRYPTION_KEY docker compose config` | Expected failure: the required key is missing; variable names are enough, no secret values in the error |
| `docker compose up --build --abort-on-container-exit migrate` | GREEN: image built, `/migrate` exit 0 on a fresh DB; a repeated run is also exit 0 |
| `docker compose up --build -d postgres nullchat` | exit 0; PostgreSQL healthy, migration exited 0, API started after its dependencies |
| `curl --fail http://127.0.0.1:8080/healthz` | HTTP 200, `{"status":"ok"}` |
| `curl --fail http://127.0.0.1:8080/readyz` | HTTP 200, `{"status":"ready"}` |
| `TEST_DATABASE_URL='postgres://nullchat:nullchat@127.0.0.1:5432/nullchat_test?sslmode=disable' go test -tags=integration -race ./internal/storage/postgres` | PASS, `13.392s` |
| `docker compose run --rm test` | PASS, PostgreSQL suite `16.509s`; builder target, integration/race, separate `nullchat_test` |
| `docker compose restart nullchat` | exit 0; then readiness HTTP 200 (`curl --fail --retry 10 --retry-connrefused --retry-delay 1 http://127.0.0.1:8080/readyz`) |
| `docker compose exec -T postgres psql -U nullchat -d nullchat -Atc 'SELECT version FROM schema_migrations'` | One row: `1` after repeated migrations |
| `docker compose exec -T postgres psql -U nullchat -d nullchat_test -Atc 'SELECT current_database()'` | `nullchat_test`; the init SQL ran on the fresh volume |

Before the Compose change, the migration acceptance command gave the expected RED: `no such service: migrate` (exit 1). After the change, a real migration and the whole workflow above were run; no new unit tests checking the YAML/Dockerfile text were added.

The first sandboxed `go test -race ./...` failed in `TestWebSocketCommandsReceiveFiniteOperationDeadline`: `httptest: failed to listen on a port: listen tcp6 [::1]:0: socket: operation not permitted`. The Docker daemon was initially unavailable with `permission denied ... /var/run/docker.sock`. A rerun with access to local sockets and Docker allowed removed both restrictions; Docker and the network were actually used, and no Compose checks were skipped.

## What the integration tests confirm

- `TestApplyMigrationsIsIdempotent`: re-applying the versioned schema.
- `TestConcurrentIdempotencyFirstUse`, `TestJoinIdempotencyReplayReturnsTheSameSession`, `TestCreateIdempotencyFingerprintConflict`, `TestJoinIdempotencyFingerprintConflict`: safe create/join retry and a conflict for a changed request; `TestJoinConsumesInviteAtomically`: a single guest under concurrency.
- `TestOperationsCommitFailureDoesNotPublish`, `TestCreateCommitFailureRollsBack`, `TestJoinCommitFailureDoesNotConsumeOrPublish`: no partial result and no publication on a commit failure.
- `TestServerRestartPreservesCommittedRoom`: the real server binary, HTTP create, WS send/ack, reading the stored message from the DB, SIGTERM/close 1001 `server_shutdown`, a new process and recovery of the previous session/history; also startup/periodic expiry and the absence of automatic migrations.
- `TestCommittedRoomSurvivesServiceRestart`: a new pool/service keeps the room, session, message and unused invite.
- The TTL, controlled snapshot/subscribe handoff, deduplication and revoke tests check the current single-process contract. `TestDatabaseFailureIsSanitizedByHTTP` checks the errors of a real closed pool; `/healthz` stays independent of the DB.

To rerun only the migration/restart cases once PostgreSQL is running:

```sh
TEST_DATABASE_URL='postgres://nullchat:nullchat@127.0.0.1:5432/nullchat_test?sslmode=disable' \
  go test -tags=integration -race ./internal/storage/postgres \
  -run 'TestApplyMigrationsIsIdempotent|TestCommittedRoomSurvivesServiceRestart|TestServerRestartPreservesCommittedRoom' -count=1 -v
```

This is a command for rerunning; in Task 8 these cases passed inside the full integration suites, not in a separate run with this filter. The tests truncate tables: use only the dedicated `nullchat_test`, and do not run suites in parallel against the same DB. Without TEST_DATABASE_URL the DB cases are skipped regardless of `-tags=integration`.

## Verification boundaries and remaining work

Durable event replay and a transactional outbox are absent. Between commit and broadcast an event can be lost for a connected client; a reconnect gets the full stored history. There are no cursors, paging, multiple cores/gateways or exactly-once delivery. TTL/end revoke access; the expiry worker deletes expired `idempotency_requests`, but physical cleanup of the remaining rows/history, WAL and the backup policy are deferred. `/readyz` checks a ping, not the schema: a successful migration is a separate startup condition.

Task 8 did not repeat the interactive browser smoke test, SIGKILL/power loss, a network blackout, load, public HTTPS or remote CI. The Compose restart verified restart/readiness; persistence of the message and session is confirmed by the process integration test, not by a new manual browser scenario. The historical browser results below refer to the memory demo of 2026-09-20.

After the verification the `nullchat-task8` project containers were stopped; the named volume and built images were kept. The verification did not delete any user data.

# Historical memory-demo verification — 2026-09-20

## Automated checks

- `go test -race ./...` — passed.
- `go vet ./...` — passed.
- `node --check web/app.js` — passed; Node is needed only for this extra check.
- Standalone binary build: `go build -buildvcs=false -o bin/nullchat ./cmd/server`.

The working environment had Go `1.27.1-X:nodwarf5`, Linux amd64. `-buildvcs=false` disables reading VCS metadata: the internal `.git` in that environment is not a complete Git repository.

## Chrome MCP

Verified in independent browser contexts:

- Creating a room and the second participant joining via the invite.
- Exchanging messages in both directions.
- Reloading the tab with the session and both messages restored.
- Forcibly closing the WebSocket with automatic reconnect and history recovery.
- Rejecting a third participant on invite reuse.
- Rendering `<script>…</script>` as plain text: no executable script nodes are created.
- `sessionStorage` does not contain message history.
- Mobile viewport 390×844: document width 390, no horizontal overflow.
- A one-minute room closes with the state “The conversation time is over”.
- An explicit end by the guest closes the creator's chat and clears the visible messages.
- No console errors/warnings in the verified scenarios.

This is a manual browser smoke test, not a permanent browser test in CI. A real network blackout, a long soak test and load have not been tested yet.

## Not verified

As of 2026-09-20, running with Docker/Compose was not verified: Docker was not available in that environment. This is a historical limitation; the actual PostgreSQL/Compose verification of 2026-09-26 is described above. Public HTTPS deployment and GitHub Actions were not run at that time.
