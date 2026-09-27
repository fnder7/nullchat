# Task 8 implementation and verification report

Date: 2026-09-26
Worktree: `/home/aidar/projects/nullchat/.worktrees/postgres-persistence`
Branch: `feature/postgres-persistence`
Base: `9faf263458d05655532499797adbf6711cc358a5`
Commit message: `docs: document PostgreSQL persistence operation`

## Delivered

- PostgreSQL 17 Compose service with `postgres_data` named volume, loopback port 5432, `pg_isready` healthcheck, and read-only init SQL mount. `deploy/postgres/init-test-db.sql` contains exactly `CREATE DATABASE nullchat_test;` plus its terminating newline.
- Explicit migration job runs `/migrate` with the application DATABASE_URL and required interpolated IDEMPOTENCY_ENCRYPTION_KEY; API requires healthy PostgreSQL and successful migration completion. Both use the same application image and secret environment variable; no encryption secret is hard-coded.
- Test profile/service builds target `build`, retains source, and executes `go test -tags=integration -race ./internal/storage/postgres` against `nullchat_test`. Added Alpine `build-base` for race/cgo support. Runtime binary builds retain Go 1.26 Alpine, CGO_ENABLED=0, -trimpath, and stripped linker flags.
- Final non-root scratch image now includes both `/nullchat` and `/migrate`, resolving the prerequisite explicitly deferred by Task 7 review.
- README includes the plan's exact five-command operator acceptance sequence, Go 1.25 floor, PostgreSQL runtime mode, explicit host migration/run commands, development-only key generation, stable deployment-secret handling, database isolation, initialization of existing volumes, and operational limits.
- SPEC and roadmap distinguish tested commit-before-ack, create/join replay, and restart persistence from deferred outbox/durable event replay, cursors/paging, physical retention, UI redesign, CI, and multiple services. Corrected the shutdown close-frame and readiness response descriptions. Roadmap steps 6–8 are not claimed complete.
- VERIFICATION records current commands/results and precise scope; retains the old memory-demo browser checks as historical evidence rather than claiming a new browser run.

## TDD, review, and scope

Read Task 8, design specification, current Dockerfile/Compose/README/SPEC/roadmap/verification, config loader, migration command, runtime, integration test helper, restart coverage, SDD ledger, and Task 7 report before implementing.

RED: after adding the README acceptance sequence but before changing Compose, `docker compose up --build --abort-on-container-exit migrate` exited 1 with `no such service: migrate`. GREEN: the same command built the image and ran `/migrate` successfully on a fresh PostgreSQL volume; startup/readiness and both host/container integration suites then passed. This used the plan's operator acceptance check as the configuration test rather than adding tests of YAML text.

The execution/TDD/verification skills guided the acceptance sequence and evidence requirements. Final review was author self-review against the task and design; no subagent reviewer tool was available. Reviewed secret interpolation, image packaging, race toolchain, database isolation, dependency ordering, startup/restart coverage, and documentation claims. No outstanding Task 8 finding.

The user's explicit scope overrides skill bookkeeping: the implementation plan and SDD ledger were not modified, no later task was started, and the SDD workspace was retained. `docs/PLAN.md` was updated as explicitly requested; it is the product roadmap, distinct from the protected implementation plan.

## Exact checks and results

Host Go checks used `GOMODCACHE=/tmp/nullchat-task4-gomod GOCACHE=/tmp/nullchat-task4-gocache` throughout. Docker checks used `COMPOSE_PROJECT_NAME=nullchat-task8` and a randomly generated development key in process environment; config output was discarded to avoid exposing that key.

1. `docker info --format '{{.ServerVersion}}'` initially failed inside the sandbox with permission denied for `/var/run/docker.sock`. Approved daemon access succeeded: Docker `29.8.0`; Compose `v5.5.1`. No pre-existing running containers or listeners on 5432/8080 were found.
2. Initial sandboxed `go test -race ./...` exited 1: `TestWebSocketCommandsReceiveFiniteOperationDeadline` panicked because httptest could not listen on `[::1]:0` (`socket: operation not permitted`). Approved local socket access resolved this environment restriction.
3. `go test -race ./...` with local socket access — exit 0, all packages passed; database-dependent tests skipped because TEST_DATABASE_URL was unset. This run is not evidence of live database behavior.
4. `go vet ./...` — exit 0, no output.
5. `go build ./cmd/server ./cmd/migrate` — exit 0, no output; checked again in a fail-fast shell.
6. `node --check web/app.js` — exit 0, no output.
7. `docker compose config` with generated key, stdout redirected to `/dev/null` — exit 0.
8. `env -u IDEMPOTENCY_ENCRYPTION_KEY docker compose config` — expected nonzero exit; required-variable diagnostics for migrate/nullchat without any secret value.
9. `docker compose up --build --abort-on-container-exit migrate` — exit 0; fresh volume/database initialized, both runtime binaries built, `/migrate` exited 0. Repeated the command successfully; the repeat log is `/tmp/nullchat-task8-migrate-repeat.log`.
10. `docker compose up --build -d postgres nullchat` — exit 0; dependencies became healthy/completed before API startup. Log: `/tmp/nullchat-task8-startup.log`.
11. `curl --fail http://127.0.0.1:8080/healthz` — HTTP 200, `{"status":"ok"}`.
12. `curl --fail http://127.0.0.1:8080/readyz` — HTTP 200, `{"status":"ready"}`.
13. `docker compose exec -T postgres psql -U nullchat -d nullchat -Atc 'SELECT version FROM schema_migrations'` — exit 0, one row `1` after repeated migrations.
14. `docker compose exec -T postgres psql -U nullchat -d nullchat_test -Atc 'SELECT current_database()'` — exit 0, `nullchat_test`.
15. `TEST_DATABASE_URL='postgres://nullchat:nullchat@127.0.0.1:5432/nullchat_test?sslmode=disable' go test -tags=integration -race ./internal/storage/postgres` — exit 0, PostgreSQL package `13.392s`.
16. `docker compose run --rm test` — exit 0, PostgreSQL package `16.509s`; actual integration/race execution inside the build image. Log: `/tmp/nullchat-task8-compose-test.log`.
17. `docker compose restart nullchat` — exit 0. `curl --fail --retry 10 --retry-connrefused --retry-delay 1 http://127.0.0.1:8080/readyz` immediately returned HTTP 200 and `{"status":"ready"}`.
18. `docker compose ps -a` — migration `Exited (0)`, API up, PostgreSQL healthy; both published ports bound to 127.0.0.1.
19. `TEST_DATABASE_URL='postgres://nullchat:nullchat@127.0.0.1:5432/nullchat_test?sslmode=disable' go test -race ./... -count=1` — exit 0, all packages passed with live database cases and no race reports; PostgreSQL package `11.421s`.
20. `docker run --rm --entrypoint go nullchat-task8-test version` — `go version go1.26.8 linux/amd64`. `docker exec nullchat-task8-postgres-1 postgres --version` — PostgreSQL `17.11`. Runtime inspection — user `65532:65532`, entrypoint `["/nullchat"]`.
21. `git diff --check` — exit 0, no output. Protected implementation-plan diff — empty; SDD ledger left untouched. Final staged whitespace/scope checks are performed immediately before the requested commit.
22. `docker stop nullchat-task8-nullchat-1 nullchat-task8-postgres-1` — exit 0. Containers created for verification are stopped; named volume and built images retained. No pre-existing database was modified or removed.
23. Final fail-fast static checks after documentation completion: `go vet ./...`, `go build ./cmd/server ./cmd/migrate`, `node --check web/app.js`, `docker compose config` (generated key, output discarded), `git diff --check`, and protected-plan diff — all exit 0. A Node assertion confirmed byte-exact init SQL and that README contains the Task 8 acceptance command block unchanged.

Live suites ran sequentially against the dedicated test database; tests apply migrations and truncate their tables. They include migration repeatability, concurrent invitation/idempotency, commit rollback, snapshot handoff, TTL/revocation, fresh service/pool restart, and real process SIGTERM/restart with a previously acknowledged message and session. The Compose restart/readiness check alone is not claimed as proof of history survival; the process integration test supplies that evidence.

## Limits

All required Docker/network checks actually ran; neither Docker verification nor integration coverage is inferred from static configuration. The initial sandbox limitations were resolved with approved access.

No new interactive browser smoke, SIGKILL/power-loss test, network blackout, load test, public HTTPS deployment, or remote CI run was performed. Durable event replay/outbox remain absent: a commit-to-broadcast failure can miss a live event until reconnect/snapshot. Physical row deletion, retention/WAL/backup policy, paging/cursors, and multi-instance delivery remain deferred.

## Fix round

Reviewer finding: the PostgreSQL healthcheck used `pg_isready` without `-h`, which probes the local Unix socket. During PostgreSQL container initialization that socket can become reachable before the TCP listener used by the migration and API services is ready, allowing Compose to advance prematurely. Changed only the healthcheck command to `pg_isready -h 127.0.0.1 -U nullchat -d nullchat`, so health now tests the same loopback TCP path. No plan, ledger, or unrelated Compose behavior changed.

Fix-round verification: `docker compose config` with a generated development key passed; the rendered healthcheck contains the exact TCP probe. `go vet ./...`, `go build ./cmd/server ./cmd/migrate`, `node --check web/app.js`, and `git diff --check` passed. The fix commit is `fix: probe PostgreSQL health over TCP`.

## Changed files

`Dockerfile`, `compose.yaml`, `deploy/postgres/init-test-db.sql`, `README.md`, `SPEC.md`, `docs/PLAN.md`, `docs/VERIFICATION.md`, and this report. The report is explicitly staged despite its ignored SDD parent directory.
