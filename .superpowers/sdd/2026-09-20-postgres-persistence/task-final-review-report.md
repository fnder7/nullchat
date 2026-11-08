# Final whole-branch review report

Base: `693e9fb`

## Outcome

All supplied final-review findings were addressed. A final self-review found and fixed one additional Important issue before completion: placing idempotency cleanup inside a room-lock expiry transaction inverted the replay lock order and could deadlock. Cleanup now runs as a separate atomic statement in the existing expiry service cycle. No Critical or Important findings remain in the reviewed diff.

No subagent/reviewer tool was available in this session, so the final fresh-context review required by the workflow was performed as an explicit author self-review. The deterministic lock-order regression and full race/integration suites cover the additional finding.

## Fixes

- Expired `idempotency_requests` are physically deleted by `Service.Expire`; expired replay remains unavailable before cleanup, and the key can be reused as new after cleanup.
- Cleanup does not hold room locks while waiting for idempotency rows, preserving replay transaction lock order.
- Graceful WebSocket shutdown writes the JSON `server_shutdown` event before close code `1001`; browser reconnect/session behavior remains unchanged.
- Global WebSocket capacity returns `connection_limit` as HTTP 429 before upgrade and as a JSON error followed by close `1013` in the post-upgrade race. Shutdown races use `server_shutdown`/`1001`, not a retryable capacity close.
- The scratch production image carries `/etc/ssl/certs/ca-certificates.crt`; README documents a combined custom CA bundle mount.
- Titles containing NUL fail with `invalid_input` during normalization, before PostgreSQL work begins.
- New migration `0002_invites_room_idx.sql` creates `invites(room_id)` without editing migration 0001.
- Product, architecture, retention, TLS, and verification documentation now match delivered behavior. Outbox/replay, paging, full history retention, and multi-service remain deferred.

## Regression evidence

- RED: NUL title accepted; connection-limit test seam/behavior absent; shutdown event absent; expired idempotency row retained; migration version 2/index absent; scratch CA bundle absent.
- RED: the first cleanup implementation reproduced a replay/expiry deadlock and returned `database_unavailable`.
- GREEN: focused chat/HTTP/WebSocket tests; focused retention, migration, and lock-order PostgreSQL integration tests; process-restart event-before-close test.

## Final verification

- `TEST_DATABASE_URL=... go test -race ./...` — PASS.
- `go vet ./...` — PASS.
- `go build ./cmd/server ./cmd/migrate` — PASS.
- `node --check web/app.js` — PASS.
- `docker compose config --quiet` — PASS.
- Docker build and migration job — PASS.
- Compose PostgreSQL/API startup plus `/healthz` and `/readyz` — PASS.
- `docker compose run --rm test` — PASS (`17.256s`).
- Live migration check — versions `1,2`; `invites_room_idx=true`.
- Final Compose image CA extraction and non-empty check — PASS.

The implementation plan, `docs/PLAN.md`, and SDD progress ledger were not modified.
