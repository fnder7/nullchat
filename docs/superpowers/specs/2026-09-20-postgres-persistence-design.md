# Nullchat PostgreSQL Persistence — Design Specification

**Status:** Draft for review  
**Date:** 2026-09-20  
**Scope:** Version 0.2 persistence foundation and restart recovery

## Goal

Replace the demo's process-local room storage with PostgreSQL while preserving the existing two-person temporary-chat flow. A room, its sessions, invitation, and committed messages must survive a backend restart until the room expires or a participant ends it.

The implementation must keep the application boundary in `internal/chat.Application`; HTTP and WebSocket transport code must not know SQL details, and PostgreSQL-specific behavior must live in `internal/storage/postgres`.

## Success criteria

- A clean PostgreSQL instance can be initialized by an explicit, repeatable migration command.
- `create`, `join`, `send`, and `end` enforce their invariants inside database transactions.
- A concurrent invitation use creates at most one guest participant.
- A repeated message with the same `(room, participant, client_message_id)` is safe and does not duplicate history.
- `ack` is sent only after the message transaction commits.
- A process restart closes WebSocket connections but does not close rooms or revoke valid sessions.
- A client can reconnect with the previous session before TTL or explicit room closure.
- Expired or explicitly closed rooms cannot be revived by an old token.
- The existing demo remains runnable with one documented command and the integration setup is reproducible with Compose.

## In scope

- PostgreSQL persistence for rooms, participants, sessions, invitations, messages, and HTTP idempotency results.
- `pgxpool` connection management and configuration validation.
- Versioned SQL migrations and an explicit migration command/service.
- Transactional implementations of `Create`, `Join`, `Send`, `End`, and authenticated snapshot/subscription support.
- Liveness/readiness separation and a stable `database_unavailable` error.
- Startup expiry recovery and periodic expiry processing.
- Minimal browser change: `server_shutdown` preserves a still-valid session and enters reconnecting state.
- Unit, contract, integration, concurrency, restart, migration, race, and vet checks.

## Out of scope

- Transactional outbox, durable event replay, or exactly-once WebSocket delivery.
- Paginated history and event cursors.
- Metrics, tracing, public rate-limit redesign, CI browser automation, or load testing.
- PostgreSQL replicas, Redis, brokers, Kubernetes, or multiple core instances.
- Groups, files, accounts, E2EE, Matrix federation, and changes to the existing product flow.
- A full redesign of failed/pending message rendering; only restart-related session preservation is required here.

## Compatibility and external contract

The existing HTTP and WebSocket routes remain available:

- `POST /api/v1/rooms` returns `201` with room, creator, session token, and invite token.
- `POST /api/v1/join` returns `200` with room, guest, and session token.
- `POST /api/v1/end` returns `200` and closes the room for both participants.
- `GET /ws` authenticates with the session token in the first WebSocket message.
- `GET /healthz` reports process liveness.
- `GET /readyz` reports whether the application can use PostgreSQL.

`POST /api/v1/rooms` and `POST /api/v1/join` accept an optional `Idempotency-Key` header. The server guarantees safe replay only when this header is present. The key is an opaque ASCII value between 32 and 128 characters; clients should generate a cryptographically random value and reuse it for a retry of the same request.

For an idempotent request, the server computes a SHA-256 fingerprint over the route and normalized request fields. The same key with the same fingerprint returns the original response within the retention window. The same key with a different fingerprint returns `409` and `idempotency_key_conflict`. A replay after the room has expired or been explicitly closed returns `410 room_unavailable` rather than reopening or reissuing access to the room.

The following error distinctions are stable at the transport boundary:

| Condition | Error code | HTTP status |
|---|---|---:|
| Invalid request | `invalid_input` | 400 |
| Invalid/revoked session | `session_invalid` | 401 |
| Forbidden Origin | `origin_forbidden` | 403 |
| Expired/closed/unavailable room or invite | `room_unavailable` | 410 |
| Message or idempotency key conflict | `message_id_conflict` / `idempotency_key_conflict` | 409 |
| Process-wide room limit reached | `capacity_reached` | 429 |
| Room already has two participants | `room_full` | 429 |
| Message limit reached | `message_limit` | 429 |
| Connection limit reached | `connection_limit` | 429 |
| Request/command budget exceeded | `rate_limited` | 429 |
| PostgreSQL is unavailable or not ready | `database_unavailable` | 503 |

`ack` means that PostgreSQL committed acceptance of the message. It does not mean delivery to the other browser or read receipt. If the process fails after commit and before a broadcast, a later reconnect obtains the committed message from the snapshot; durable event replay is intentionally deferred.

## Architecture

The dependency direction remains:

```text
cmd/server
  ├── internal/httpapi -> internal/chat.Application
  ├── internal/storage/postgres -> internal/chat models and realtime hub
  └── web
```

`cmd/server` loads configuration, opens the pool, constructs the PostgreSQL application, starts the HTTP server and expiry worker, then shuts down transport and pool without invoking the business `End` operation.

`internal/storage/postgres` implements the complete application scenarios. It must not expose `Get`/`Save` primitives to HTTP. Each scenario owns the transaction that protects its authorization, capacity, TTL, deduplication, and state transition rules.

The existing realtime hub remains an in-process delivery mechanism. A PostgreSQL transaction commits before the service publishes an event. Since the hub is not durable, reconnect/snapshot is the recovery mechanism for missed broadcasts in this stage.

## Data model

All primary IDs are generated by the application as cryptographically random opaque strings. Session and invitation secrets are generated in plaintext only for the response, while PostgreSQL stores their SHA-256 hashes.

### `rooms`

- `id` primary key
- `title` non-null, normalized title
- `status` constrained to `open`, `closed`, or `expired`
- `expires_at` non-null timestamptz
- `closed_at` nullable timestamptz
- `next_message_sequence` non-null integer, initialized to `1`
- `message_count` non-null integer, initialized to `0`
- `created_at` non-null timestamptz

Indexes: `rooms(expires_at)` for expiry work. The open-room capacity check is serialized with a transaction-level advisory lock so two concurrent creates cannot both pass the process-wide room limit.

### `participants`

- `id` primary key
- `room_id` foreign key to `rooms`
- `slot` constrained to `1` or `2`
- `name` non-null
- unique `(room_id, slot)` and unique `(room_id, name)`
- unique `(room_id, id)` for composite participant ownership checks

### `sessions`

- `token_hash` primary key
- `participant_id` foreign key to `participants`
- `room_id` foreign key to `rooms`
- `expires_at` non-null timestamptz
- `revoked_at` nullable timestamptz
- `last_send_at` nullable timestamptz

Indexes support lookup by participant/room and removal of expired or revoked sessions.

### `invites`

- `token_hash` primary key
- `room_id` foreign key to `rooms`
- `expires_at` non-null timestamptz
- `consumed_at` nullable timestamptz

### `messages`

- `id` primary key
- `room_id` foreign key to `rooms`
- `participant_id` foreign key to `participants`
- `client_message_id` non-null
- `sequence` non-null integer
- `text` non-null UTF-8 payload within the application byte limit
- `created_at` non-null timestamptz
- unique `(room_id, sequence)`
- unique `(room_id, participant_id, client_message_id)`

The participant-to-room relationship is enforced by a composite foreign key from `(room_id, participant_id)` to `participants(room_id, id)`, so a participant from another room cannot insert a message into this room. The same relationship is used for sessions.

### `idempotency_requests`

- `key_hash` primary key
- `endpoint` non-null
- `request_fingerprint` non-null
- `response_ciphertext` non-null
- `room_id` nullable foreign key to `rooms`
- `created_at` and `expires_at` non-null timestamptz

The response envelope is encrypted with AES-GCM using a deployment key from configuration. Plain session/invite tokens must never be written to logs. Retention is bounded by the room lifetime and at most 24 hours after the original request.

## Transaction semantics

### Create

1. Begin a transaction and acquire the fixed advisory lock for the process-wide room capacity.
2. Check/replay the idempotency record when `Idempotency-Key` is present.
3. Normalize and validate title/TTL.
4. Count active rooms under the capacity lock; reject with `capacity_reached` if the limit is reached.
5. Insert the room, creator participant, hashed invite, hashed session, and encrypted idempotency response.
6. Commit; only then return credentials.

### Join

1. Begin a transaction and check/replay the idempotency record when supplied.
2. Select the invite and its room `FOR UPDATE`.
3. Check status and expiry using the database server time.
4. Reject consumed/expired invites without disclosing room details.
5. Select the available participant slot, insert the guest, mark the invite consumed, create the hashed session, and store the encrypted replay response.
6. Commit; only then publish the participant event.

### Send

1. Begin a transaction and resolve the session to its room/participant.
2. Lock the room row `FOR UPDATE`; after acquiring the lock, recheck session validity, room status, and `expires_at` against database time.
3. Normalize the text with the existing application rule (`strings.TrimSpace`, preserving all other Unicode code points). Look up `(room_id, participant_id, client_message_id)`. Return the existing message when normalized text matches; return `message_id_conflict` otherwise.
4. Enforce message count and per-session send interval.
5. Allocate `sequence` from `next_message_sequence`, insert the message, increment the room counters, and update `last_send_at`.
6. Commit; only then return the message for `ack` and publish the best-effort realtime event.

### End

1. Begin a transaction, resolve the session, and lock its room row.
2. Recheck session validity and room status.
3. Set status to `closed`, set `closed_at`, revoke all room sessions, and consume/revoke the invite.
4. Commit; only then publish `room_closed` to connected subscribers.

### Expiry and restart

Every authenticated scenario checks TTL independently of the cleanup worker. The expiry worker periodically claims expired open rooms, marks them `expired`, revokes sessions/invites, and publishes `room_expired` after commit. The same work runs after startup before normal expiry polling; retries are safe because the state transition is conditional on `status = 'open'`.

Graceful shutdown cancels the worker, closes WebSockets through the transport shutdown path, waits for HTTP requests, and closes the pool. It does not mark rooms closed. After restart, the first snapshot is read from PostgreSQL and therefore includes committed history.

Subscribe preserves the existing no-gap guarantee with a short database transaction: it validates the session, locks the room row in share mode, reads the snapshot, registers the in-process subscription, emits the snapshot into its bounded queue, and commits. `Send`, `Join`, `End`, and expiry transitions lock the same room row for update. A write that commits before the share lock is acquired is present in the snapshot; a write that commits afterward publishes to the already-registered subscriber.

## Configuration and operations

Configuration is parsed once at startup and rejects invalid values without logging secrets. Required persistence settings include `DATABASE_URL` and the AES-GCM idempotency encryption key. `ADDR` keeps the current loopback default for local use. Pool size, operation timeout, room/message/connection limits, and retention durations have explicit defaults matching the current contract and can be overridden by named environment variables.

Migrations are plain versioned SQL files embedded in the migration binary. `cmd/migrate` applies them in order and records versions in a migration table. Applying an already-installed version is a no-op. The API process never runs migrations automatically. Compose starts PostgreSQL with a persistent volume and healthcheck, then runs the migration service before the API service.

`/healthz` does not query PostgreSQL and remains available while the process is alive. `/readyz` pings the pool and reports `503 database_unavailable` until the database is usable. Protected operations also map pool/query failures to `database_unavailable` and never expose driver error text.

## Testing strategy and acceptance

- Shared application contract tests run against both the memory adapter and PostgreSQL adapter where semantics are common.
- Migration tests create a clean database, apply all versions twice, and verify the second run is unchanged.
- Concurrent join tests issue many requests for one invite and assert one guest at most.
- Concurrent send tests assert one message for a repeated client ID, one conflict for changed text, and strictly increasing room sequences.
- Transaction tests cover rollback before commit and assert no partial room, participant, invite, or message remains.
- Race tests cover send/end/expiry contention and run with `go test -race ./...`.
- Integration tests stop and restart the backend against the same PostgreSQL instance, then reconnect with the old session and verify room/history survival.
- Expiry tests cover a room expiring while idle and a request arriving after TTL but before the cleanup worker.
- HTTP tests cover idempotency replay, fingerprint conflict, readiness, stable error mapping, and absence of credentials in application logs.
- Final verification runs `go test -race ./...`, `go vet ./...`, migration/integration tests with PostgreSQL, a clean Compose startup, and the existing browser smoke flow.

## Deferred decisions

The following are deliberately not solved by this specification: durable event outbox and replay, cursor-based history synchronization, retention/WAL backup policy beyond the stated database retention boundary, multi-instance ownership, public abuse controls, and performance targets. Each requires a separate design and acceptance contract rather than being implied by PostgreSQL persistence.
