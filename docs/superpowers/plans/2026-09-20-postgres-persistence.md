# PostgreSQL Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist nullchat rooms in PostgreSQL so committed rooms, sessions, and messages survive a backend restart while preserving the existing HTTP/WebSocket flow.

**Architecture:** Keep `internal/chat.Application` as the transport-independent scenario port. Add a PostgreSQL implementation in `internal/storage/postgres`, backed by `pgxpool`, and make HTTP/WebSocket depend only on chat input/output types and stable sentinel errors. PostgreSQL transactions serialize room state changes; the existing realtime hub remains an in-process, best-effort notifier and a reconnect snapshot recovers committed history.

**Tech Stack:** Go 1.25, `github.com/jackc/pgx/v5` v5.11.0 and `pgxpool`, PostgreSQL 17, Docker Compose, embedded versioned SQL migrations, existing `github.com/coder/websocket`.

**Spec:** `docs/superpowers/specs/2026-09-20-postgres-persistence-design.md`

## Global Constraints

- Preserve `internal/chat.Application`; HTTP and WebSocket transport code must not import PostgreSQL packages or SQL details.
- Use `pgxpool` with explicit SQL and versioned SQL migrations; the API process never applies migrations automatically.
- Require Go 1.25 or later because pgx/v5.11.0 supports Go 1.25+; keep Docker's Go image at or above that floor.
- Generate all IDs and session/invite secrets cryptographically; store only SHA-256 token hashes in PostgreSQL and never log plaintext credentials.
- Keep room TTL at 1–60 minutes, default 30; TTL starts at creation and does not extend on activity.
- Enforce 100 active rooms, two participants per room, 500 messages per room, 4,000 UTF-8 bytes per normalized message, and a 200 ms per-session send interval.
- An `ack` is emitted only after the corresponding PostgreSQL transaction commits; it is neither delivery nor read confirmation.
- `Idempotency-Key` is optional, ASCII, 32–128 bytes; if supplied, the same endpoint/key/fingerprint replays the encrypted original response until the room closes or at most 24 hours after creation.
- Expiry, `End`, and graceful process shutdown are different: expiry/end revoke room access; shutdown closes transport and the database pool without ending rooms.
- Keep committed data accessible after restart, but do not add a transactional outbox, cursor replay, pagination, CI automation, or multiple service instances in this plan.

## Review Focus

- Same idempotency key with a changed request must return `idempotency_key_conflict`, never credentials for a different request. Covered by Task 5's `TestCreateIdempotencyFingerprintConflict` and `TestJoinIdempotencyFingerprintConflict`.
- Dozens of concurrent uses of one invite must create exactly one guest. Covered by Task 5's `TestJoinConsumesInviteAtomically`.
- A message waiting on a room lock must still be rejected if TTL passes before the lock is acquired. Covered by Task 6's `TestSendRechecksExpiryAfterRoomLock`.
- A database outage must make `/readyz` and protected operations return `database_unavailable` without exposing a driver error. Covered by Task 4's `TestReadyAndDatabaseUnavailable`.
- A message at the snapshot/subscription boundary must appear exactly once across snapshot and live events. Covered by Task 6's `TestSubscribeAndSendHaveNoGap`.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/chat/application.go` | Transport-neutral input structs, errors, limits, and the scenario interface. |
| `internal/chat/validation.go` | Shared title/message normalization and random participant-name generation. |
| `internal/config/config.go` | Environment parsing and validation without logging secrets. |
| `internal/storage/postgres/migrations/0001_initial.sql` | Initial PostgreSQL schema, constraints, and indexes. |
| `internal/storage/postgres/migrate.go` | Embedded migration runner shared by `cmd/migrate` and integration tests. |
| `internal/storage/postgres/service.go` | PostgreSQL `chat.Application` implementation, transactions, expiry, and hub coordination. |
| `internal/storage/postgres/idempotency.go` | AES-GCM response encryption and idempotency record handling. |
| `internal/storage/postgres/*_test.go` | Database-backed migration, scenario, race, snapshot, and restart tests. |
| `internal/httpapi/server.go` | `Idempotency-Key`, `/readyz`, and stable HTTP error mapping. |
| `cmd/migrate/main.go` | Explicit migration executable. |
| `cmd/server/main.go` | PostgreSQL pool/service wiring and non-destructive shutdown. |
| `web/app.js` | Reusable browser idempotency keys for create/join requests. |
| `deploy/postgres/init-test-db.sql` | Compose initialization of the isolated integration-test database. |
| `compose.yaml`, `Dockerfile`, `README.md` | Reproducible local PostgreSQL startup, migration ordering, and operator guidance. |

### Task 1: Define the persistence-aware chat contract

**Files:**

- Create: `internal/chat/validation.go`
- Create: `internal/chat/validation_test.go`
- Modify: `internal/chat/application.go`
- Modify: `internal/storage/memory/service.go`
- Modify: `internal/storage/memory/service_test.go`
- Modify: `internal/httpapi/server_test.go`

**Interfaces:**

- Produces: `chat.CreateInput`, `chat.JoinInput`, `chat.NormalizeTitle`, `chat.NormalizeMessage`, `chat.GenerateParticipantName`, and sentinel errors used by every later task.
- Produces: `chat.Application` methods `Create(context.Context, CreateInput)`, `Join(context.Context, JoinInput)`, `Subscribe(context.Context, string)`, `Send(context.Context, string, string, string)`, `End(context.Context, string)`, and `Ready(context.Context)`.

- [ ] **Step 1: Write failing chat validation and input-contract tests**

Create `internal/chat/validation_test.go` with the exact boundary cases and update memory/http tests to construct the new inputs:

```go
func TestNormalizeTitleAndMessage(t *testing.T) {
    title, err := NormalizeTitle("  Вечер  ")
    if err != nil || title != "Вечер" { t.Fatalf("title = %q, %v", title, err) }
    empty, err := NormalizeTitle("  ")
    if err != nil || empty != "Quiet conversation" { t.Fatalf("empty = %q, %v", empty, err) }
    body, err := NormalizeMessage("client-1", "  привет  ")
    if err != nil || body != "привет" { t.Fatalf("body = %q, %v", body, err) }
    if _, err := NormalizeMessage("", "hello"); !errors.Is(err, ErrInput) { t.Fatal(err) }
    if _, err := NormalizeMessage("id", string(make([]byte, MaxTextBytes+1))); !errors.Is(err, ErrInput) { t.Fatal(err) }
}

func TestCreateInputKeepsIdempotencyKey(t *testing.T) {
    input := CreateInput{Title: "Room", TTLMinutes: 30, IdempotencyKey: "12345678901234567890123456789012"}
    if input.IdempotencyKey == "" { t.Fatal("key lost") }
}
```

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `go test ./internal/chat ./internal/storage/memory ./internal/httpapi`

Expected: FAIL because the new input types, validation functions, readiness method, and error names do not exist.

- [ ] **Step 3: Add input structs, error distinctions, and shared validation**

In `internal/chat/application.go`, replace the primitive create/join parameters and add the exact public types/errors:

```go
var (
    ErrUnavailable         = errors.New("room_unavailable")
    ErrSession             = errors.New("session_invalid")
    ErrInput               = errors.New("invalid_input")
    ErrCapacity            = errors.New("capacity_reached")
    ErrRoomFull            = errors.New("room_full")
    ErrMessageLimit        = errors.New("message_limit")
    ErrConnectionLimit     = errors.New("connection_limit")
    ErrRate                = errors.New("rate_limited")
    ErrConflict            = errors.New("message_id_conflict")
    ErrIdempotencyConflict = errors.New("idempotency_key_conflict")
    ErrDatabaseUnavailable = errors.New("database_unavailable")
)

type CreateInput struct { Title string; TTLMinutes int; IdempotencyKey string }
type JoinInput struct { InviteToken string; IdempotencyKey string }

type Application interface {
    Create(context.Context, CreateInput) (Credentials, error)
    Join(context.Context, JoinInput) (Credentials, error)
    Subscribe(context.Context, string) (<-chan Event, func(), error)
    Send(context.Context, string, string, string) (Message, error)
    End(context.Context, string) error
    Ready(context.Context) error
}
```

Create `validation.go` with title/message rules currently duplicated in the memory service. `NormalizeTitle` trims whitespace, rejects invalid UTF-8 or more than 60 runes, and substitutes `Quiet conversation` for empty input. `NormalizeMessage` trims whitespace and rejects an empty/invalid client ID, client IDs longer than 64 bytes, invalid UTF-8, an empty normalized body, or a body over `MaxTextBytes` bytes. Move `generatedNames` and its crypto-random uniqueness selection here as `GenerateParticipantName(existing []Participant) (string, error)`.

Update the memory adapter to use those functions, accept `CreateInput`/`JoinInput`, preserve `ErrUnavailable` for a consumed or missing invitation, return `ErrMessageLimit` at 500 messages, and implement `Ready(context.Context) error { return nil }`. Preserve its existing demo-only behavior for idempotency keys; PostgreSQL implements persisted replay in Task 5.

- [ ] **Step 4: Update callers and compile-time implementation assertion**

Change the memory test helper and HTTP handler callers to use:

```go
c, err := s.Create(testContext, chat.CreateInput{Title: "Test room", TTLMinutes: 30})
guest, err := s.Join(testContext, chat.JoinInput{InviteToken: c.InviteToken})
```

Keep `var _ chat.Application = (*Service)(nil)` at the end of the memory service. This makes interface drift fail at compilation.

- [ ] **Step 5: Run focused tests to verify they pass**

Run: `go test ./internal/chat ./internal/storage/memory ./internal/httpapi`

Expected: PASS; legacy memory behavior still works through the new contract and the new validation boundaries pass.

- [ ] **Step 6: Commit the contract change**

```bash
git add internal/chat/application.go internal/chat/validation.go internal/chat/validation_test.go internal/storage/memory/service.go internal/storage/memory/service_test.go internal/httpapi/server_test.go
git commit -m "refactor: define persistence-aware chat contract"
```

### Task 2: Add configuration and the PostgreSQL driver baseline

**Files:**

- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `Dockerfile`
- Modify: `README.md`

**Interfaces:**

- Consumes: Go 1.25 and `github.com/jackc/pgx/v5` v5.11.0.
- Produces: `config.Config` and `config.Load(getenv func(string) string) (Config, error)` for migration and server commands.

- [ ] **Step 1: Write failing configuration tests**

Create `internal/config/config_test.go` with a table-driven environment helper:

```go
func TestLoad(t *testing.T) {
    env := map[string]string{
        "DATABASE_URL": "postgres://nullchat:secret@localhost:5432/nullchat?sslmode=disable",
        "IDEMPOTENCY_ENCRYPTION_KEY": strings.Repeat("ab", 32),
    }
    cfg, err := Load(func(key string) string { return env[key] })
    if err != nil { t.Fatal(err) }
    if cfg.Addr != "127.0.0.1:8080" || cfg.MaxConns != 8 || cfg.OperationTimeout != 5*time.Second { t.Fatalf("%+v", cfg) }
    env["IDEMPOTENCY_ENCRYPTION_KEY"] = "bad"
    if _, err := Load(func(key string) string { return env[key] }); err == nil { t.Fatal("accepted short key") }
    delete(env, "DATABASE_URL")
    if _, err := Load(func(key string) string { return env[key] }); err == nil { t.Fatal("accepted missing database URL") }
}
```

- [ ] **Step 2: Run the configuration test to verify it fails**

Run: `go test ./internal/config`

Expected: FAIL because package `internal/config` does not exist.

- [ ] **Step 3: Pin the supported toolchain and pgx dependency**

Run:

```bash
go mod edit -go=1.25.0
go get github.com/jackc/pgx/v5@v5.11.0
go mod tidy
```

Keep the Docker builder at its already-supported Go version and build both required executables with the existing reproducible flags:

```dockerfile
FROM golang:1.26-alpine AS build
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nullchat ./cmd/server
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate
```

Update the README floor from Go 1.24 to Go 1.25 and state that local PostgreSQL mode additionally needs `DATABASE_URL` and `IDEMPOTENCY_ENCRYPTION_KEY`.

- [ ] **Step 4: Implement strict configuration parsing**

Implement these exact defaults and validation rules:

```go
type Config struct {
    Addr string
    DatabaseURL string
    IdempotencyKey [32]byte
    MaxConns int32
    OperationTimeout time.Duration
    ExpiryInterval time.Duration
}

func Load(getenv func(string) string) (Config, error) {
    cfg := Config{Addr: value(getenv, "ADDR", "127.0.0.1:8080"), MaxConns: 8, OperationTimeout: 5*time.Second, ExpiryInterval: time.Second}
    cfg.DatabaseURL = getenv("DATABASE_URL")
    if cfg.DatabaseURL == "" { return Config{}, errors.New("DATABASE_URL is required") }
    raw, err := hex.DecodeString(getenv("IDEMPOTENCY_ENCRYPTION_KEY"))
    if err != nil || len(raw) != len(cfg.IdempotencyKey) { return Config{}, errors.New("IDEMPOTENCY_ENCRYPTION_KEY must be 64 hex characters") }
    copy(cfg.IdempotencyKey[:], raw)
    return cfg, nil
}
```

Do not include the URL or encryption key in returned errors or logs. Parse optional `DB_MAX_CONNS`, `DB_OPERATION_TIMEOUT`, and `EXPIRY_INTERVAL` only when supplied; reject non-positive values.

- [ ] **Step 5: Run formatting and focused tests**

Run: `gofmt -w internal/config/config.go internal/config/config_test.go && go test ./internal/config && go mod tidy && git diff --check`

Expected: PASS; `go.mod` contains Go 1.25 and pgx v5.11.0, and config tests prove no secret is embedded in an error.

- [ ] **Step 6: Commit configuration and dependency baseline**

```bash
git add go.mod go.sum Dockerfile README.md internal/config/config.go internal/config/config_test.go
git commit -m "feat: add PostgreSQL configuration baseline"
```

### Task 3: Create embedded, repeatable PostgreSQL migrations

**Files:**

- Create: `internal/storage/postgres/migrations/0001_initial.sql`
- Create: `internal/storage/postgres/migrate.go`
- Create: `internal/storage/postgres/migrate_test.go`
- Create: `internal/storage/postgres/testdb_test.go`
- Create: `cmd/migrate/main.go`

**Interfaces:**

- Consumes: `config.Config.DatabaseURL` and `pgxpool.Pool` from Task 2.
- Produces: `postgres.ApplyMigrations(context.Context, *pgxpool.Pool) error` and an explicit `go run ./cmd/migrate` command.

- [ ] **Step 1: Write failing migration idempotency test**

Create `internal/storage/postgres/migrate_test.go`; use `TEST_DATABASE_URL` and skip only when it is not set:

```go
func TestApplyMigrationsIsIdempotent(t *testing.T) {
    pool := openTestPool(t)
    if err := ApplyMigrations(testContext, pool); err != nil { t.Fatal(err) }
    if err := ApplyMigrations(testContext, pool); err != nil { t.Fatal(err) }
    var count int
    if err := pool.QueryRow(testContext, `SELECT count(*) FROM schema_migrations WHERE version = 1`).Scan(&count); err != nil { t.Fatal(err) }
    if count != 1 { t.Fatalf("version rows = %d", count) }
}
```

`openTestPool` must read `TEST_DATABASE_URL`, call `t.Skip` if it is empty, ping the pool with a five-second context, apply no migration itself, and truncate `idempotency_requests, messages, sessions, invites, participants, rooms RESTART IDENTITY CASCADE` during cleanup. Define `var testContext = context.Background()` in this test helper package.

- [ ] **Step 2: Run the test to verify it fails when a test database is configured**

Run: `TEST_DATABASE_URL='postgres://nullchat:secret@127.0.0.1:5432/nullchat_test?sslmode=disable' go test -tags=integration ./internal/storage/postgres -run TestApplyMigrationsIsIdempotent -v`

Expected: FAIL because the postgres package and migration runner do not exist. If the URL is unavailable, the test is SKIP; record that Compose Task 8 supplies the same URL.

- [ ] **Step 3: Write the initial schema migration**

Create `0001_initial.sql` with the full durable model:

```sql
CREATE TABLE rooms (
  id text PRIMARY KEY,
  title text NOT NULL,
  status text NOT NULL CHECK (status IN ('open', 'closed', 'expired')),
  expires_at timestamptz NOT NULL,
  closed_at timestamptz,
  next_message_sequence integer NOT NULL DEFAULT 1 CHECK (next_message_sequence > 0),
  message_count integer NOT NULL DEFAULT 0 CHECK (message_count >= 0),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX rooms_expires_at_idx ON rooms (expires_at);

CREATE TABLE participants (
  id text PRIMARY KEY,
  room_id text NOT NULL REFERENCES rooms(id),
  slot smallint NOT NULL CHECK (slot IN (1, 2)),
  name text NOT NULL,
  UNIQUE (room_id, slot), UNIQUE (room_id, name), UNIQUE (room_id, id)
);
CREATE TABLE sessions (
  token_hash bytea PRIMARY KEY,
  participant_id text NOT NULL,
  room_id text NOT NULL,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  last_send_at timestamptz,
  FOREIGN KEY (room_id, participant_id) REFERENCES participants(room_id, id)
);
CREATE INDEX sessions_room_idx ON sessions (room_id);
CREATE TABLE invites (
  token_hash bytea PRIMARY KEY,
  room_id text NOT NULL REFERENCES rooms(id),
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz
);
CREATE TABLE messages (
  id text PRIMARY KEY,
  room_id text NOT NULL,
  participant_id text NOT NULL,
  client_message_id text NOT NULL,
  sequence integer NOT NULL CHECK (sequence > 0),
  text text NOT NULL,
  created_at timestamptz NOT NULL,
  FOREIGN KEY (room_id) REFERENCES rooms(id),
  FOREIGN KEY (room_id, participant_id) REFERENCES participants(room_id, id),
  UNIQUE (room_id, sequence), UNIQUE (room_id, participant_id, client_message_id)
);
CREATE INDEX messages_room_sequence_idx ON messages (room_id, sequence);
CREATE TABLE idempotency_requests (
  key_hash bytea PRIMARY KEY,
  endpoint text NOT NULL,
  request_fingerprint bytea NOT NULL,
  response_ciphertext bytea NOT NULL,
  room_id text REFERENCES rooms(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);
CREATE INDEX idempotency_requests_expires_at_idx ON idempotency_requests (expires_at);
```

- [ ] **Step 4: Implement the embedded migration runner and command**

Use `//go:embed migrations/*.sql`, derive numeric versions from the sorted `0001_*.sql` filenames, and acquire one dedicated `pgxpool.Conn` before migration work. On that connection execute `SELECT pg_advisory_lock(982463117)`, defer `SELECT pg_advisory_unlock(982463117)`, create/read the ledger, and run each unapplied migration in its own transaction. The runner must create this ledger before reading versions:

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (
  version bigint PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
)
```

Expose:

```go
func ApplyMigrations(ctx context.Context, pool *pgxpool.Pool) error
```

`cmd/migrate/main.go` loads `config.Load(os.Getenv)`, opens a pool with `pgxpool.New`, defers `pool.Close()`, calls `postgres.ApplyMigrations`, and exits non-zero with only the wrapped operation name on failure. It must not print `DATABASE_URL`.

- [ ] **Step 5: Run migration tests and command build**

Run:

```bash
go test ./internal/storage/postgres -run TestApplyMigrationsIsIdempotent
go test -tags=integration ./internal/storage/postgres -run TestApplyMigrationsIsIdempotent -v
go build ./cmd/migrate
```

Expected: the first command skips without `TEST_DATABASE_URL`; the integration command passes with PostgreSQL; the migration binary builds.

- [ ] **Step 6: Commit migration infrastructure**

```bash
git add internal/storage/postgres/migrations/0001_initial.sql internal/storage/postgres/migrate.go internal/storage/postgres/migrate_test.go internal/storage/postgres/testdb_test.go cmd/migrate/main.go
git commit -m "feat: add PostgreSQL schema migrations"
```

### Task 4: Expose readiness, idempotency headers, and stable transport errors

**Files:**

- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/server_test.go`
- Modify: `web/app.js`

**Interfaces:**

- Consumes: `chat.CreateInput`, `chat.JoinInput`, `chat.Application.Ready`, and all sentinel errors from Task 1.
- Produces: `GET /readyz`, `Idempotency-Key` propagation, and reusable browser keys for retrying unchanged create/join requests.

- [ ] **Step 1: Write failing HTTP tests for readiness and header propagation**

Add a small spy application in `server_test.go` that records `CreateInput`/`JoinInput` and returns `chat.ErrDatabaseUnavailable` from `Ready`. Test:

```go
func TestReadyAndDatabaseUnavailable(t *testing.T) {
    app := &spyApplication{readyErr: chat.ErrDatabaseUnavailable}
    h := New(app).Handler(web.Handler())
    ready := httptest.NewRecorder()
    h.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
    if ready.Code != http.StatusServiceUnavailable || !strings.Contains(ready.Body.String(), "database_unavailable") { t.Fatal(ready.Body.String()) }
    req := httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader(`{"title":"R"}`))
    req.Header.Set("Idempotency-Key", "12345678901234567890123456789012")
    w := httptest.NewRecorder(); h.ServeHTTP(w, req)
    if app.create.IdempotencyKey != req.Header.Get("Idempotency-Key") { t.Fatal("header was not forwarded") }
}
```

Also add table rows proving invalid keys (31 bytes, 129 bytes, and non-ASCII) return `400`, `ErrRoomFull` maps to `429 room_full`, `ErrMessageLimit` maps to `429 message_limit`, `ErrConnectionLimit` maps to `429 connection_limit`, and `ErrDatabaseUnavailable` maps to `503`.

- [ ] **Step 2: Run the HTTP test to verify it fails**

Run: `go test ./internal/httpapi -run 'TestReadyAndDatabaseUnavailable|TestHTTPValidationAndOrigin' -v`

Expected: FAIL because `/readyz`, key validation/forwarding, and the error mappings are absent.

- [ ] **Step 3: Implement HTTP readiness and idempotency key handling**

Add the route and exact helpers:

```go
mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
    if err := s.chat.Ready(r.Context()); err != nil { fail(w, chat.ErrDatabaseUnavailable); return }
    respond(w, http.StatusOK, map[string]string{"status": "ready"})
})

func idempotencyKey(r *http.Request) (string, error) {
    key := r.Header.Get("Idempotency-Key")
    if key == "" { return "", nil }
    if len(key) < 32 || len(key) > 128 { return "", chat.ErrInput }
    for _, c := range key { if c < 0x21 || c > 0x7e { return "", chat.ErrInput } }
    return key, nil
}
```

`create` and `join` must validate the header before calling `s.chat` and pass it in `chat.CreateInput`/`chat.JoinInput`. Extend `fail` with `ErrRoomFull`, `ErrMessageLimit`, `ErrConnectionLimit`, `ErrIdempotencyConflict`, and `ErrDatabaseUnavailable`; only `ErrDatabaseUnavailable` maps to 503. Keep `GET /healthz` independent of `Ready`.

- [ ] **Step 4: Make browser create/join retries reuse keys**

Add request key state and pass it as a header without persisting it in `sessionStorage`:

```js
const requestKeys = { create: '', join: '' };
function retryKey(kind) { return requestKeys[kind] || (requestKeys[kind] = crypto.randomUUID()); }
async function api(path, data, token, key = '') {
  const headers = { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}), ...(key ? { 'Idempotency-Key': key } : {}) };
  const response = await fetch(path, { method: 'POST', headers, body: JSON.stringify(data), signal: AbortSignal.timeout(10000) });
  const result = await response.json();
  if (!response.ok) throw new Error(errors[result.error] || 'Не удалось выполнить запрос. Попробуй ещё раз.');
  return result;
}
```

Call `api` for create with `retryKey('create')` and for join with `retryKey('join')`; clear the corresponding key only after success. On title/TTL `input` events clear `requestKeys.create`, so a materially changed request receives a new key instead of a deliberate fingerprint conflict.

- [ ] **Step 5: Run HTTP and frontend syntax checks**

Run: `go test ./internal/httpapi && node --check web/app.js`

Expected: PASS; HTTP tests show the contract and the browser script parses.

- [ ] **Step 6: Commit transport changes**

```bash
git add internal/httpapi/server.go internal/httpapi/server_test.go web/app.js
git commit -m "feat: add PostgreSQL readiness and idempotency transport"
```

### Task 5: Implement PostgreSQL room creation, joining, and replay-safe credentials

**Files:**

- Create: `internal/storage/postgres/service.go`
- Create: `internal/storage/postgres/idempotency.go`
- Create: `internal/storage/postgres/create_join_test.go`

**Interfaces:**

- Consumes: schema/migration API from Task 3, `chat.CreateInput` and `chat.JoinInput` from Task 1, and `config.Config.IdempotencyKey` from Task 2.
- Produces: `postgres.New(pool *pgxpool.Pool, key [32]byte) *Service`, `Create`, `Join`, `Subscribe`, and encrypted idempotency replay behavior used by Task 6 and runtime wiring.

- [ ] **Step 1: Write failing PostgreSQL admission and replay tests**

Create database-backed tests using `openTestPool(t)` and `ApplyMigrations`:

```go
func TestJoinConsumesInviteAtomically(t *testing.T) {
    s := newTestService(t)
    creator, err := s.Create(testContext, chat.CreateInput{Title: "R", TTLMinutes: 30})
    if err != nil { t.Fatal(err) }
    var success atomic.Int32
    var wg sync.WaitGroup
    for range 32 { wg.Add(1); go func() { defer wg.Done(); if _, err := s.Join(testContext, chat.JoinInput{InviteToken: creator.InviteToken}); err == nil { success.Add(1) } else if !errors.Is(err, chat.ErrUnavailable) { t.Error(err) } }() }
    wg.Wait()
    if success.Load() != 1 { t.Fatalf("joined = %d", success.Load()) }
}

func TestCreateIdempotencyFingerprintConflict(t *testing.T) {
    s := newTestService(t)
    key := strings.Repeat("a", 32)
    first, err := s.Create(testContext, chat.CreateInput{Title: "R", TTLMinutes: 30, IdempotencyKey: key})
    if err != nil { t.Fatal(err) }
    replay, err := s.Create(testContext, chat.CreateInput{Title: "R", TTLMinutes: 30, IdempotencyKey: key})
    if err != nil || replay.SessionToken != first.SessionToken || replay.InviteToken != first.InviteToken { t.Fatalf("replay = %+v, %v", replay, err) }
    _, err = s.Create(testContext, chat.CreateInput{Title: "other", TTLMinutes: 30, IdempotencyKey: key})
    if !errors.Is(err, chat.ErrIdempotencyConflict) { t.Fatal(err) }
}
```

Add `TestJoinIdempotencyReplayReturnsTheSameSession`, `TestRoomCapacityUsesAnAdvisoryLock`, `TestInviteDoesNotRevealRoomAfterUse`, and `TestCredentialsAreNotStoredInPlaintext` (inspect `sessions.token_hash`, `invites.token_hash`, and ensure neither returned token appears in `idempotency_requests.response_ciphertext`).

- [ ] **Step 2: Run the admission test to verify it fails**

Run: `go test -tags=integration ./internal/storage/postgres -run 'TestJoinConsumesInviteAtomically|TestCreateIdempotencyFingerprintConflict' -v`

Expected: FAIL because `postgres.Service` does not exist.

- [ ] **Step 3: Implement token, name, and encrypted replay helpers**

In `idempotency.go`, generate 32 random bytes for session/invite tokens, encode as 64 lowercase hex characters, hash database keys with `sha256.Sum256`, and create an AES-GCM instance from the 32-byte configuration key. Encrypt/decrypt only a JSON `chat.Credentials` envelope; include a new random nonce in every ciphertext.

Use these exact idempotency lookup rules inside the transaction:

```go
SELECT endpoint, request_fingerprint, response_ciphertext, room_id, expires_at
FROM idempotency_requests WHERE key_hash = $1 FOR UPDATE
```

Before that lookup, when an idempotency key is present, acquire a transaction-level advisory lock derived from the first eight bytes of its SHA-256 digest. This serializes concurrent first uses of the same previously unseen key. If no row exists, continue. If endpoint or fingerprint differs, return `chat.ErrIdempotencyConflict`. If `expires_at <= now()` or the linked room is no longer `open`/unexpired, return `chat.ErrUnavailable`. Otherwise decrypt and return the original credentials. Store the record with `expires_at = LEAST(room.expires_at, now() + interval '24 hours')`.

- [ ] **Step 4: Implement transactional `Create` and `Join`**

`Create` must normalize/validate `input.Title` and construct the endpoint/fingerprint before beginning work. It then begins a transaction with `pool.Begin(ctx)`, takes the key-specific advisory lock when supplied, executes `SELECT pg_advisory_xact_lock(982463118)`, checks replay, enforces `chat.MaxRooms` with `SELECT count(*) FROM rooms WHERE status = 'open' AND expires_at > now()`, then inserts room, slot-1 participant, hashed invite/session, and optional idempotency record before `tx.Commit`.

`Join` must validate the token shape, construct its endpoint/fingerprint, begin a transaction, take the key-specific advisory lock when supplied, check replay, then lock the invite and room in this order, so all conflict paths have the same lock order:

```sql
SELECT i.token_hash, i.room_id, i.consumed_at, r.status, r.expires_at
FROM invites i JOIN rooms r ON r.id = i.room_id
WHERE i.token_hash = $1
FOR UPDATE OF i, r
```

Reject a missing/consumed/expired invite with `chat.ErrUnavailable`; check slot availability, generate a unique name using `chat.GenerateParticipantName`, insert slot 2 and its session, set `consumed_at = now()`, persist the optional encrypted replay response, commit, then publish a `participants` event. Do not publish while a transaction is open.

Implement `Subscribe` in the same file: begin a read-write transaction, resolve the valid session, lock the room `FOR SHARE`, read participants/messages ordered by `sequence`, register a hub subscription while the lock is held, enqueue `chat.SnapshotEvent`, commit, and return the channel/unsubscribe closure. On any failure after hub registration, unsubscribe before returning. This lock protocol is the required no-gap handoff.

- [ ] **Step 5: Run admission, replay, and snapshot tests**

Run: `go test -tags=integration ./internal/storage/postgres -run 'TestJoinConsumesInviteAtomically|TestCreateIdempotency|TestJoinIdempotency|TestRoomCapacity|TestCredentialsAreNotStoredInPlaintext' -v`

Expected: PASS; exactly one join succeeds, same-key responses are byte-for-byte equivalent credentials, changed requests conflict, and database rows do not contain plaintext tokens.

- [ ] **Step 6: Commit PostgreSQL admission scenarios**

```bash
git add internal/storage/postgres/service.go internal/storage/postgres/idempotency.go internal/storage/postgres/create_join_test.go
git commit -m "feat: persist room creation and joining in PostgreSQL"
```

### Task 6: Add transactional send, terminal transitions, expiry, and no-gap coverage

**Files:**

- Modify: `internal/storage/postgres/service.go`
- Create: `internal/storage/postgres/send_end_test.go`

**Interfaces:**

- Consumes: `postgres.Service` admission/session lookup from Task 5 and `realtime.Hub` from the existing project.
- Produces: complete `chat.Application` implementation, `Expire(context.Context) error`, and `var _ chat.Application = (*Service)(nil)`.

- [ ] **Step 1: Write failing send/end/expiry tests**

Create `send_end_test.go` with these direct scenario checks:

```go
func TestDuplicateMessageIsStoredOnce(t *testing.T) {
    s, creator := newCreatedService(t)
    first, err := s.Send(testContext, creator.SessionToken, "stable-id", " hello ")
    if err != nil { t.Fatal(err) }
    again, err := s.Send(testContext, creator.SessionToken, "stable-id", "hello")
    if err != nil || again.ID != first.ID || again.Sequence != 1 { t.Fatalf("again = %+v, %v", again, err) }
    if _, err := s.Send(testContext, creator.SessionToken, "stable-id", "changed"); !errors.Is(err, chat.ErrConflict) { t.Fatal(err) }
}

func TestSubscribeAndSendHaveNoGap(t *testing.T) {
    for range 50 {
        s, creator := newCreatedService(t)
        start := make(chan struct{}); done := make(chan struct{})
        go func() { <-start; _, _ = s.Send(testContext, creator.SessionToken, "one", "race"); close(done) }()
        close(start)
        events, cancel, err := s.Subscribe(testContext, creator.SessionToken)
        if err != nil { t.Fatal(err) }
        <-done
        snapshot := (<-events).Payload.(chat.SnapshotEvent)
        count := len(snapshot.Room.Messages)
        select { case event := <-events: if event.Type == "message" { count++ }; default: }
        cancel()
        if count != 1 { t.Fatalf("message count = %d", count) }
    }
}
```

Add tests for `End` revoking both sessions/invite, `Expire` revoking access without a prior sweep, 500 messages yielding `ErrMessageLimit`, sends under 200 ms yielding `ErrRate`, concurrent sends yielding increasing sequences, and `TestSendRechecksExpiryAfterRoomLock` using a transaction-held row lock plus an already-expired `expires_at`.

- [ ] **Step 2: Run the focused tests to verify they fail**

Run: `go test -tags=integration ./internal/storage/postgres -run 'TestDuplicateMessageIsStoredOnce|TestSubscribeAndSendHaveNoGap|TestEndRevokes|TestSendRechecksExpiryAfterRoomLock' -v`

Expected: FAIL because `Send`, `End`, and `Expire` are incomplete.

- [ ] **Step 3: Implement `Send` with a room-row lock and post-commit publish**

Within one transaction, resolve the token hash, lock its room, and recheck all terminal conditions after that lock:

```sql
SELECT r.id, r.status, r.expires_at, r.next_message_sequence, r.message_count,
       p.id, s.last_send_at
FROM sessions s
JOIN participants p ON p.id = s.participant_id
JOIN rooms r ON r.id = s.room_id
WHERE s.token_hash = $1 AND s.revoked_at IS NULL
FOR UPDATE OF r, s
```

Normalize the message with `chat.NormalizeMessage`. Query the unique client ID before rate/count checks; matching normalized text returns the stored row, differing text returns `chat.ErrConflict`. For a new message, reject `status != 'open'` or `expires_at <= now()` with `chat.ErrSession`, reject `message_count >= chat.MaxMessages` with `chat.ErrMessageLimit`, reject an interval below 200 ms with `chat.ErrRate`, insert sequence `next_message_sequence`, increment both room counters, update `last_send_at`, commit, and only then `hub.Publish` a `message` event.

- [ ] **Step 4: Implement end, expiry, readiness, and shutdown-safe cleanup**

Implement a shared `closeRoomTx(ctx, tx, roomID, reason)` that conditionally transitions only `status = 'open'`, sets `closed_at`, updates every room session `revoked_at = now()`, and sets `invites.consumed_at = COALESCE(consumed_at, now())`. `End` validates the caller before calling it and, after commit, calls `hub.CloseRoom` with `room_closed`. `Expire` claims batches with `FOR UPDATE SKIP LOCKED`, applies the same helper with reason `expired`, commits each batch transaction, then calls `hub.CloseRoom` with `room_expired`.

Implement:

```go
func (s *Service) Ready(ctx context.Context) error {
    if err := s.pool.Ping(ctx); err != nil { return chat.ErrDatabaseUnavailable }
    return nil
}
func (s *Service) Close() { s.hub.CloseAll() }
var _ chat.Application = (*Service)(nil)
```

Add `Hub.CloseAll()` in `internal/realtime/hub.go`; it closes active subscriptions without publishing a terminal room event. Map pool/query cancellation and connection failures to `chat.ErrDatabaseUnavailable`, but preserve `context.Canceled` and `context.DeadlineExceeded` when the caller's context caused them.

- [ ] **Step 5: Run database scenarios with the race detector**

Run: `go test -tags=integration -race ./internal/storage/postgres -v`

Expected: PASS; no duplicate messages, terminal transitions revoke access, snapshot/live handoff has no gap, and the race detector reports no data races.

- [ ] **Step 6: Commit the complete PostgreSQL application adapter**

```bash
git add internal/storage/postgres/service.go internal/storage/postgres/send_end_test.go
git commit -m "feat: add transactional PostgreSQL chat operations"
```

### Task 7: Wire the server to PostgreSQL and prove restart recovery

**Files:**

- Modify: `cmd/server/main.go`
- Create: `internal/storage/postgres/restart_test.go`
- Modify: `internal/httpapi/server_test.go`
- Modify: `internal/realtime/hub.go`
- Modify: `internal/realtime/hub_test.go`
- Modify: `docs/ARCHITECTURE.md`

**Interfaces:**

- Consumes: `config.Load`, `postgres.New`, `postgres.ApplyMigrations` as an operator-only command, and `Service.Expire` from Tasks 2–6.
- Produces: a runtime that never calls room-ending logic during graceful shutdown and reconnects to persisted state after a new process/service instance is created.

- [ ] **Step 1: Write failing restart and readiness integration tests**

Create `restart_test.go` to persist state through a fresh pool/service:

```go
func TestCommittedRoomSurvivesServiceRestart(t *testing.T) {
    pool := openTestPool(t); requireMigrations(t, pool)
    first := New(pool, testEncryptionKey())
    creator, err := first.Create(testContext, chat.CreateInput{Title: "Restart", TTLMinutes: 30})
    if err != nil { t.Fatal(err) }
    message, err := first.Send(testContext, creator.SessionToken, "one", "stored")
    if err != nil { t.Fatal(err) }
    first.Close()
    second := New(pool, testEncryptionKey())
    events, cancel, err := second.Subscribe(testContext, creator.SessionToken)
    if err != nil { t.Fatal(err) }; defer cancel()
    snapshot := (<-events).Payload.(chat.SnapshotEvent)
    if len(snapshot.Room.Messages) != 1 || snapshot.Room.Messages[0].ID != message.ID { t.Fatal("history was not restored") }
}
```

Extend HTTP tests to assert `/healthz` remains 200 while a spy application's `Ready` returns `chat.ErrDatabaseUnavailable` and that protected failures do not return driver text.

Add the realtime shutdown test required by the new lifecycle method:

```go
func TestHubCloseAllClosesSubscriptionsWithoutTerminalEvent(t *testing.T) {
    h := New()
    sub, ok := h.Subscribe("room", "session", 2)
    if !ok { t.Fatal("subscribe failed") }
    h.CloseAll()
    if _, ok := <-sub.Events; ok { t.Fatal("subscription remains open") }
}
```

- [ ] **Step 2: Run restart coverage to verify it fails**

Run: `go test -tags=integration ./internal/storage/postgres -run TestCommittedRoomSurvivesServiceRestart -v`

Expected: FAIL because the service has no explicit lifecycle and `cmd/server` still creates memory storage and calls `Close` as a business shutdown.

- [ ] **Step 3: Replace memory runtime wiring without auto-migrating**

Refactor `cmd/server/main.go` to load config, parse a `pgxpool.Config`, apply `cfg.MaxConns`, open and ping the pool with `cfg.OperationTimeout`, then construct `postgres.New(pool, cfg.IdempotencyKey)`. Do not call `postgres.ApplyMigrations` from this binary.

Use a worker that calls `service.Expire` on `cfg.ExpiryInterval` and exits on signal cancellation. During shutdown, call `api.Shutdown()`, `server.Shutdown(shutdownCtx)`, cancel the expiry worker, then `service.Close()` only to release hub subscriptions and `pool.Close()` only to release connections. Do not invoke `End` or any function that updates room status.

Preserve the existing `server_shutdown` WebSocket event. Verify that `web/app.js`'s current branch leaves `sessionStorage` untouched and lets `onclose` schedule reconnect; do not add a second terminal-state path.

- [ ] **Step 4: Record the implemented boundary in the architecture document**

Update `docs/ARCHITECTURE.md` so it states that the active adapter is PostgreSQL, `Shutdown` only stops infrastructure, expiry/end perform persistent logical closure, and the realtime hub is best-effort until the separate outbox design. Remove claims that the current implementation is only in-memory once this wiring lands.

- [ ] **Step 5: Run runtime/restart checks**

Run:

```bash
go test ./internal/httpapi
go test -tags=integration ./internal/storage/postgres -run TestCommittedRoomSurvivesServiceRestart -v
go build ./cmd/server ./cmd/migrate
```

Expected: PASS; the runtime builds, old sessions see their committed snapshot after a fresh service instance, and health/readiness remain distinct.

- [ ] **Step 6: Commit PostgreSQL runtime wiring**

```bash
git add cmd/server/main.go internal/storage/postgres/restart_test.go internal/httpapi/server_test.go internal/realtime/hub.go internal/realtime/hub_test.go docs/ARCHITECTURE.md
git commit -m "feat: run nullchat on PostgreSQL storage"
```

### Task 8: Make PostgreSQL startup reproducible and document the delivered contract

**Files:**

- Modify: `compose.yaml`
- Modify: `README.md`
- Modify: `SPEC.md`
- Modify: `docs/PLAN.md`
- Modify: `docs/VERIFICATION.md`
- Create: `deploy/postgres/init-test-db.sql`

**Interfaces:**

- Consumes: explicit `cmd/migrate`, required environment variables, server readiness, and integration test tag from prior tasks.
- Produces: a Compose topology with database health, one explicit migration job, API readiness dependency, and documented verification commands.

- [ ] **Step 1: Write the reproducible operator commands as README acceptance checks**

Add this exact development sequence to the README before changing Compose:

```bash
export IDEMPOTENCY_ENCRYPTION_KEY="$(openssl rand -hex 32)"
docker compose up --build --abort-on-container-exit migrate
docker compose up --build -d postgres nullchat
curl --fail http://127.0.0.1:8080/readyz
TEST_DATABASE_URL='postgres://nullchat:nullchat@127.0.0.1:5432/nullchat_test?sslmode=disable' go test -tags=integration -race ./internal/storage/postgres
```

State that this key is development-only and must be generated/stored as a deployment secret outside the repository.

- [ ] **Step 2: Update Compose with ordered database, migration, API, and test services**

Define PostgreSQL 17 with the `nullchat` database, a named data volume, and `pg_isready` healthcheck. Mount `deploy/postgres/init-test-db.sql` into `/docker-entrypoint-initdb.d/`; its exact content is `CREATE DATABASE nullchat_test;`. Define `migrate` to use the built image with `entrypoint: ["/migrate"]`, the production database URL, and `IDEMPOTENCY_ENCRYPTION_KEY`. Define `nullchat` to depend on PostgreSQL health and successful migration completion. Pass the same secret by environment variable; never hard-code it in Compose. Define a `test` profile/service built from Dockerfile target `build`, with source available and `TEST_DATABASE_URL` set to `nullchat_test`, running `go test -tags=integration -race ./internal/storage/postgres`.

The relevant dependency shape must be:

```yaml
nullchat:
  depends_on:
    postgres:
      condition: service_healthy
    migrate:
      condition: service_completed_successfully
```

- [ ] **Step 3: Update product documentation to distinguish delivered and deferred guarantees**

In `SPEC.md`, mark PostgreSQL commit-before-ack, idempotent create/join, and restart persistence as implemented only after their tests pass. Keep outbox/replay, paging, and multi-service work explicitly deferred. In `docs/PLAN.md`, mark the completed 0.2 foundation items accurately without claiming steps 6–8 are complete. In `docs/VERIFICATION.md`, add commands/results for migrations, Postgres integration tests, readiness, restart recovery, and the known absence of durable event replay.

- [ ] **Step 4: Run end-to-end verification**

Run:

```bash
go test -race ./...
go vet ./...
go build ./cmd/server ./cmd/migrate
node --check web/app.js
docker compose config
docker compose up --build --abort-on-container-exit migrate
docker compose up --build -d postgres nullchat
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8080/readyz
docker compose run --rm test
```

Expected: all Go/static checks pass; migration exits successfully; both health endpoints return 200 after startup; the integration test service passes. If Docker is unavailable in the execution environment, run all Go checks, record the unavailable external prerequisite in `docs/VERIFICATION.md`, and do not claim Compose was verified.

- [ ] **Step 5: Commit operations and documentation**

```bash
git add compose.yaml README.md SPEC.md docs/PLAN.md docs/VERIFICATION.md deploy/postgres/init-test-db.sql
git commit -m "docs: document PostgreSQL persistence operation"
```

## Plan self-review

### Spec coverage

| Spec requirement | Plan task |
|---|---|
| pgxpool, config validation, Go/toolchain compatibility | Task 2 |
| Versioned explicit migrations and database schema | Task 3 |
| Idempotent create/join with encrypted credentials | Tasks 4–5 |
| Atomic create/join/send/end and stable errors | Tasks 1, 4–6 |
| Snapshot/subscription no-gap behavior | Tasks 5–6 |
| TTL, expiry worker, and non-destructive shutdown | Tasks 6–7 |
| Restart recovery and browser session preservation | Tasks 4 and 7 |
| Compose, documentation, and reproducible verification | Task 8 |
| Outbox, cursors, CI, multi-instance work remain out of scope | Global Constraints and Task 8 documentation |

No spec requirement is intentionally omitted.

### Placeholder scan

The plan contains no `TBD`, `TODO`, “implement later”, “fill in details”, or unspecified validation/error-handling steps. Every task names concrete files, interfaces, tests, commands, and commit contents.

### Type consistency

`chat.CreateInput`, `chat.JoinInput`, `chat.Application`, `postgres.New`, `postgres.ApplyMigrations`, `config.Load`, `Service.Expire`, and the sentinel errors are defined before tasks consume them. The PostgreSQL adapter is not asserted as `chat.Application` until Task 6 completes all required methods.

### Review Focus coverage

All five Review Focus risks have an owning test task: changed idempotency fingerprints and concurrent invites in Task 5; post-lock expiry and snapshot/live boundaries in Task 6; database readiness/error masking in Task 4.
