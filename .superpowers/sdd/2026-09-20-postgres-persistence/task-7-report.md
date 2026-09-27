# Task 7 implementation and verification report

Date: 2026-09-26
Worktree: `/home/aidar/projects/nullchat/.worktrees/postgres-persistence`
Base: `1164ccd7253e2a06c1b27a95fd20fe5445e1bac9`
Commit message: `feat: run nullchat on PostgreSQL storage`

## Implementation

- Runtime loads `config.Load`, parses the pgx pool configuration, sets MaxConns and ConnectTimeout, and opens/pings with OperationTimeout. It constructs `postgres.New` and never applies migrations. Startup database errors are sanitized.
- Expiry runs once before serving and on ExpiryInterval, with a bounded operation context and cancellation. Failed expiry attempts log a generic warning and retry on the next tick.
- Shutdown stops transport and the worker before deferred Service.Close and pool.Close. Service.Close releases only hub subscriptions. There is no shutdown End call or room-status update. Listener failure also follows cleanup before returning a nonzero exit status.
- Fresh-pool/service integration coverage proves the complete committed room/message snapshot, creator identity/session and unused invitation survive restart. A separate binary-level test proves an HTTP-created room and WebSocket-acknowledged message persist in PostgreSQL, SIGTERM returns close code 1001 with reason `server_shutdown`, and the old session reconnects to its committed history in a new process.
- The binary-level test also verifies periodic expiry, startup expiry with a one-hour polling interval, and no automatic migrations in an isolated empty schema. Protected HTTP operations in that schema return only `database_unavailable`.
- Health/readiness coverage asserts liveness does not call Ready and readiness recovers. A real closed-pool HTTP test covers health, readiness, create, join, and authenticated end with exact sanitized response bodies.
- Retained Task 6's Hub.CloseAll implementation and `TestHubCloseAllClosesSubscriptionsWithoutTerminalEvent`, unchanged. Updated ARCHITECTURE.md to describe the active PostgreSQL adapter, explicit migrations, infrastructure-only shutdown, persistent end/expiry, and best-effort realtime without outbox.

## TDD and scope decisions

- The plan's service-only restart test already passes on Task 6 because lifecycle support was completed there (as recorded in the existing ledger). Added the process test before changing production code to expose the actual missing Task 7 behavior.
- RED: the new process test failed with `acknowledged message was not committed to PostgreSQL: no rows in result set` against the memory runtime. GREEN: it passed after runtime wiring. Fresh-service recovery and sanitized HTTP failures passed before the wiring change and remain regression coverage for existing behavior.
- Shutdown preserves the existing HTTP transport's close frame (`1001`, `server_shutdown`) and reconnect behavior. It does not add a JSON shutdown event. Read `web/app.js`: its existing `server_shutdown` branch leaves saved sessions intact, and `onclose` schedules reconnect for this close code. No browser or HTTP production changes were needed.
- Execution, TDD, and verification skills guided the work. The explicit user scope overrides ledger updates and advancement to Task 8. Plan, ledger, Compose, README, frontend, PostgreSQL Service, and realtime implementation/tests remain unchanged.

## Exact checks

All successful Go commands used:

`GOMODCACHE=/tmp/nullchat-task4-gomod GOCACHE=/tmp/nullchat-task4-gocache`

Live commands additionally used:

`TEST_DATABASE_URL=postgres://nullchat:nullchat@127.0.0.1:55437/nullchat_test?sslmode=disable`

1. Initial `go test ./...` could not initialize the default build cache because `/home/aidar/.cache/go-build` is read-only. Retried with the writable caches above.
2. Sandboxed baseline `go test ./...` then failed only because httptest could not create a local TCP socket (`operation not permitted`). Approved execution with local socket access passed the full baseline; database-backed cases skipped at baseline because TEST_DATABASE_URL was unset.
3. Live RED: `go test -tags=integration ./internal/storage/postgres -run 'TestCommittedRoomSurvivesServiceRestart|TestServerRestartPreservesCommittedRoom|TestDatabaseFailureIsSanitizedByHTTP' -count=1 -v` — exit 1, expected missing persistent message in the memory runtime; the other two tests passed.
4. Live GREEN: same command after wiring — exit 0, all three tests passed (PostgreSQL package 1.319s).
5. Final focused checks: `go test -tags=integration ./internal/httpapi ./internal/realtime ./internal/storage/postgres -run 'TestReadyAndDatabaseUnavailable|TestHTTPTransportErrorMappings|TestWebSocket|TestHubCloseAll|TestCommittedRoomSurvivesServiceRestart|TestServerRestartPreservesCommittedRoom|TestDatabaseFailureIsSanitizedByHTTP' -count=1 -v` — exit 0, every selected test passed; includes the completed no-auto-migration test.
6. Live full suite: `go test ./... -count=1` — exit 0, all packages passed; PostgreSQL package 10.037s.
7. Live full integration/race suite: `GOFLAGS=-race go test -tags=integration -race ./... -count=1` — exit 0, all packages passed, no race reports; PostgreSQL package 27.565s. GOFLAGS also instruments the server binary built by the process test.
8. `go vet ./...` — exit 0, no output.
9. `go build ./cmd/server ./cmd/migrate` — exit 0, no output.
10. `gofmt -w cmd/server/main.go internal/storage/postgres/restart_test.go internal/httpapi/server_test.go` applied; final `gofmt -l` on those files produced no output.
11. `git diff --check` — exit 0, no output. A staged whitespace check is also performed immediately before committing.

## Environment and review limitations

- TEST_DATABASE_URL was initially unset and no PostgreSQL container was running. Approved execution started the disposable PostgreSQL 17 Alpine container `nullchat-task7-postgres` on loopback port 55437; all live checks used only that isolated test database. The container was stopped after verification, with data retained. No existing database was modified.
- The sandbox required writable Go caches and approved local network access for tests; these restrictions were resolved. No live database verification remains skipped or blocked.
- Performed author self-review against Task 7, the design, existing ledger rulings, shutdown ordering, and the new tests. No outstanding Task 7 issue found. No independent reviewer tool is available.
- Browser behavior was inspected in source; no interactive browser smoke or Task 8 Compose run was performed. The new process test exercises the actual HTTP/WebSocket shutdown and reconnect path.
- Only the four changed Task 7 files and this requested report are included in the commit. The report is explicitly staged despite the surrounding SDD directory being ignored.

## Fix round: review findings

Addressed the three Important findings while preserving the chat application interface:

- `httpapi.NewWithOperationTimeout` applies `DB_OPERATION_TIMEOUT` to each finite HTTP readiness/protected operation and each WebSocket `Subscribe` setup or `Send` command. The returned subscription and socket retain the uncapped connection context after setup. Timeout failures are sanitized to `database_unavailable` when the caller itself has not been canceled.
- Upgraded WebSocket handlers are registered with cancellation functions and a completion wait group. `Server.Shutdown` marks admission closed, completes the existing `server_shutdown` close handshake, cancels remaining active handlers, and waits for handler completion before returning. A regression test blocks `Subscribe`, verifies its setup deadline does not become a socket-lifetime deadline, and proves shutdown releases and waits for it.
- Subscription `database_unavailable` and connection-limit failures now close with retryable status `1013`; permanent authentication/session failures retain `1008`. `web/app.js` explicitly treats `1013` as retryable and keeps session storage intact. Transport and frontend tests cover the status and credential-preservation contract.

Fix-round TDD evidence:

1. RED: focused tests initially failed to compile because `NewWithOperationTimeout` did not exist. After implementation, the first shutdown regression exposed a cancellation-aware test-double issue, then exposed graceful-close/`CloseNow` contention. The fixture and lifecycle ordering were corrected separately.
2. GREEN: `go test ./internal/httpapi ./web -run 'TestProtectedOperationsReceiveFiniteOperationDeadline|TestWebSocketCommandsReceiveFiniteOperationDeadline|TestShutdownCancelsBlockedSubscribeAndWaitsForHandler|TestDatabaseUnavailableSubscriptionIsRetryable|TestFrontendRetriesTransientWebSocketCloseWithoutDeletingSession' -count=1 -v` — exit 0.

Final fix-round checks:

1. `TEST_DATABASE_URL=postgres://nullchat:nullchat@127.0.0.1:55438/nullchat_test?sslmode=disable go test ./... -count=1` — exit 0.
2. `TEST_DATABASE_URL=postgres://nullchat:nullchat@127.0.0.1:55438/nullchat_test?sslmode=disable go test -tags=integration -race ./... -count=1` — exit 0; no race reports.
3. `go vet ./...` — exit 0.
4. `go build ./cmd/server ./cmd/migrate` — exit 0.
5. `gofmt -l cmd/server/main.go internal/httpapi/server.go internal/httpapi/server_test.go web/app_test.go` — no output.
6. `git diff --check` — exit 0.

The fix-round database checks used the disposable PostgreSQL 17 container `nullchat-task7-fix-postgres` on loopback port 55438; it was created only for these tests and is stopped after verification. The pre-existing Dockerfile omission of the `/migrate` build remains explicitly deferred to Task 8 and was not modified. No plan, ledger, Compose, or other Task 8 files were changed.

### Fix-round takeover completion

Validation of the inherited uncommitted changes found and corrected three gaps before commit:

- `cmd/server` still constructed the transport with `httpapi.New`, leaving the production operation timeout at zero. It now passes `cfg.OperationTimeout` through a tested runtime constructor.
- WebSocket `Subscribe` setup was unbounded. It now receives its own finite operation context, which is canceled after setup; the socket and returned subscription continue under the deadline-free connection context. Each later `Send` receives a fresh operation context.
- Shutdown canceled the socket read context before the 1001 close handshake, allowing the websocket library to close the TCP connection abruptly. `Server.Shutdown` now completes and joins close handshakes first, cancels any remaining application handlers, and joins those handlers before returning. The PostgreSQL-backed process regression changed from EOF to close code 1001 with `server_shutdown`.

Takeover RED/GREEN evidence:

1. RED: `go test ./cmd/server ./internal/httpapi -run 'TestRuntimeAPIUsesConfiguredOperationTimeout|TestWebSocketCommandsReceiveFiniteOperationDeadline|TestShutdownCancelsBlockedSubscribeAndWaitsForHandler' -count=1 -v` — exit 1: `newAPI` was undefined and both WebSocket tests reported that subscription setup had no finite deadline.
2. GREEN: the same focused runtime/WebSocket cases passed after production timeout wiring and context separation.
3. RED: `TEST_DATABASE_URL=postgres://nullchat:nullchat@127.0.0.1:55438/nullchat_test?sslmode=disable go test -tags=integration ./internal/storage/postgres -run 'TestServerRestartPreservesCommittedRoom|TestCommittedRoomSurvivesServiceRestart' -count=1 -v` — exit 1 because the restart process received EOF instead of the required 1001 `server_shutdown` close.
4. GREEN: the same integration command after lifecycle ordering was corrected — exit 0; both tests passed (`1.211s`).

Final takeover verification:

1. Focused runtime/transport/frontend command covering configured runtime timeout, HTTP timeout, finite Subscribe/Send contexts, blocked-Subscribe shutdown join, retryable database close, and frontend credential preservation — exit 0; `cmd/server` `0.026s`, `internal/httpapi` `0.083s`, `web` `0.031s`.
2. `TEST_DATABASE_URL=postgres://nullchat:nullchat@127.0.0.1:55438/nullchat_test?sslmode=disable go test ./... -count=1` — exit 0; all packages passed, PostgreSQL `9.412s`.
3. `TEST_DATABASE_URL=postgres://nullchat:nullchat@127.0.0.1:55438/nullchat_test?sslmode=disable go test -tags=integration -race ./... -count=1` — exit 0; all packages passed with no race report, PostgreSQL `11.611s`.
4. `go vet ./...` — exit 0, no output.
5. `go build ./cmd/server ./cmd/migrate` — exit 0, no output.
6. `node --check web/app.js` — exit 0, no output.
7. `gofmt -l cmd/server/main.go cmd/server/main_test.go internal/httpapi/server.go internal/httpapi/server_test.go web/app_test.go` — exit 0, no output.
8. `git diff --check` — exit 0, no output.

The disposable `nullchat-task7-fix-postgres` container was reused for the takeover verification and stopped afterward. Dockerfile, Compose, the plan, and the ledger remain unchanged; the `/migrate` image prerequisite stays deferred to Task 8.
