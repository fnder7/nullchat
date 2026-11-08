# nullchat

A temporary chat for two with no sign-up. A custom Go backend, WebSocket and a small web interface embedded in the executable. Matrix is not used.

Create a room → send a one-time link → talk until the timer runs out.

## Running

The quick start needs Docker with the Compose plugin and OpenSSL. Running and testing directly on the host needs Go 1.25 or newer (pgx v5.11.0) and PostgreSQL 17. The first build downloads external dependencies. The server works with PostgreSQL; there is no mode without a database.

Quick start of the whole local environment (ports 5432 and 8080 must be free):

```bash
./run.sh
```

On first run the script creates `.env` with a local key, then builds and starts PostgreSQL, the migrations and the application. Logs stay in the current terminal; press `Ctrl+C` to stop. PostgreSQL data persists in a named volume between runs.

The exact manual acceptance sequence for development:

```bash
export IDEMPOTENCY_ENCRYPTION_KEY="$(openssl rand -hex 32)"
docker compose up --build --abort-on-container-exit migrate
docker compose up --build -d postgres nullchat
curl --fail http://127.0.0.1:8080/readyz
TEST_DATABASE_URL='postgres://nullchat:nullchat@127.0.0.1:5432/nullchat_test?sslmode=disable' go test -tags=integration -race ./internal/storage/postgres
```

The key generated here is for development only. For a deployment, generate and store a separate secret outside the repository and pass it through the environment from a secret store. `IDEMPOTENCY_ENCRYPTION_KEY` is 32 random bytes as 64 hex characters, shared by the migrations and the API. Keep the same key across restarts: replacing it makes the stored encrypted create/join responses unreadable. Do not commit the key or print it to logs; the output of `docker compose config` also contains the substituted secrets.

The first `up` applies the migrations in the foreground; a successful exit of the migration job ends the command. The second one starts the database and the API. The API waits for a healthy database and a successful migration. `/readyz` checks the database connection, `/healthz` only that the process is alive; readiness does not replace migration and does not check the schema. If a request right after `up -d` arrived before the port opened, repeat it.

Compose uses the local credentials `nullchat:nullchat` and `sslmode=disable` for development only. A deployment needs its own credentials, TLS and connection settings. The scratch image contains the system CA roots for ordinary PostgreSQL TLS verification. For a private CA, build a single PEM bundle with the public and internal CAs and mount it read-only over `/etc/ssl/certs/ca-certificates.crt` (or at another path with `SSL_CERT_FILE`); do not disable certificate verification. PostgreSQL keeps its data in the named volume `postgres_data`; ordinary `stop`, `restart` and `down` preserve it. `down -v` removes the volume along with the data. The script `deploy/postgres/init-test-db.sql` creates `nullchat_test` only when an empty volume is first initialized. For an existing volume without the test database, create it separately: `docker compose exec postgres createdb -U nullchat nullchat_test`.

To run the Go server on the host, leave only PostgreSQL running, set both required parameters and apply the migrations explicitly:

```sh
docker compose stop nullchat
docker compose up -d postgres
export DATABASE_URL='postgres://nullchat:nullchat@127.0.0.1:5432/nullchat?sslmode=disable'
# IDEMPOTENCY_ENCRYPTION_KEY is already set above or loaded from a secret store.
go run ./cmd/migrate
go run ./cmd/server
```

Migrations are embedded in `cmd/migrate` and applied by version under an advisory lock; a repeated run is a no-op for versions already applied. The server does not run migrations itself.

Open [http://127.0.0.1:8080](http://127.0.0.1:8080). Create a room and open the invite in another browser, a private window or a separate tab. To check the TTL, choose "1 minute".

The session is stored in the `sessionStorage` of a specific tab. Reloading the page keeps access; copying the `#room=…` address does not transfer the session. Use the "Copy link" button for the invite.

A different address:

```sh
ADDR=127.0.0.1:9000 go run ./cmd/server
```

Build both binaries (the environment with the database and the key is still required):

```sh
go build -buildvcs=false -o bin/nullchat ./cmd/server
go build -buildvcs=false -o bin/migrate ./cmd/migrate
./bin/migrate
./bin/nullchat
```

The final Docker image contains `/nullchat` with the embedded interface, `/migrate` with the embedded SQL migrations and the system CA bundle. Both binaries are static; the API and the migration job run without root.

Settings: `ADDR` is `127.0.0.1:8080` on the host (`0.0.0.0:8080` in the image), `DB_MAX_CONNS` is 8, `DB_OPERATION_TIMEOUT` is `5s`, `EXPIRY_INTERVAL` is `1s`. The connection count and the intervals must be positive. Room/message limits are not configured by these variables.

A public deployment needs an HTTPS reverse proxy. By default the demo is reachable only on loopback. The frontend and the API are served from one origin; Node.js and a separate frontend server are not needed to run it.

## What works

- Rooms for two with a TTL of 1 to 60 minutes, counted from creation.
- Separate sessions for the creator and the guest; the invite is used once.
- Messaging over WebSocket, receipt acknowledgements and duplicate protection.
- Automatic reconnection and history recovery for an active room.
- PostgreSQL keeps rooms, sessions, invites and acknowledged messages across backend restarts. Graceful shutdown does not end the conversation.
- `ack` is issued after the message is committed to PostgreSQL; it does not confirm delivery to or reading by the contact.
- Create/join support an optional `Idempotency-Key` (32–128 printable ASCII characters); the browser retries the request with the same key. A matching request returns the original credentials, a changed one gets `idempotency_key_conflict`.
- Closing the room on TTL or by command of either participant.
- Bounded event queues, heartbeat, basic limits and graceful shutdown.
- End-to-end encryption of messages and the title in the browser, an optional room password and a key safety code.
- A responsive English interface, error states and chat ending.

## Demo limits

One backend instance with PostgreSQL and a local event hub. At most 100 active rooms, 500 messages per room and 400 connections. A reconnect receives the whole history. Outbox, durable event replay, paging, cursors and multiple instances/services are deferred. A failure after commit but before broadcast can leave a connected client without the event until it reconnects; the snapshot restores the stored messages. Durability depends on the PostgreSQL and disk settings.

A session lives until the room ends, even if the tab is closed. Returning after closing depends on whether the browser restores its `sessionStorage`; reloading an existing tab is guaranteed. Unsent messages are kept only in the memory of the open page. Retrying create/join is safe only with the same key and request, before the room ends and no more than 24 hours after the request. Without a key, a lost response is not recovered automatically.

The service does not ask for personal data and does not write application access logs. Messages and the title are encrypted in the browser (AES-256-GCM, a key from ephemeral ECDH P-256 authenticated by the secret from the link and an optional password). The server and the database see only ciphertext, sizes to within 256 bytes, times and participants. Web E2EE trusts the code the server serves: if the server is compromised, it can serve modified JavaScript. Session/invite tokens are indexed by SHA-256, idempotency responses are encrypted with AES-GCM. The expiry worker physically deletes expired `idempotency_requests` rows; after deletion the same key can be used as a new one. TTL/end revoke access, but the other room rows are not deleted: full physical cleanup, history retention, WAL and backup policy are not implemented yet. A participant can save the conversation; reverse proxy logging is a separate matter. Who sees what: [SECURITY.md](docs/SECURITY.md).

## End-to-end encryption

- The link secret (`#…&k=…`) never goes to the server. With a password, the link alone is not enough: the server checks `join_proof` without learning the password, and after 5 failed attempts revokes the invite.
- The message key appears once the guest joins, so you cannot write before that. The safety code in the sidebar must match on both sides: compare it by voice if ruling out interception matters.
- Keys are created non-extractable and stay in IndexedDB until the room ends (or until the next page open after it ends). A script injected into the page can use them but not copy them. CSP with Trusted Types blocks HTML injection and `eval`.
- Details and the threat model: [E2EE design](docs/superpowers/specs/2026-11-06-e2ee-design.md).

### Frontend verification

To make sure the deployed service serves the same code as the repository:

```sh
go run ./cmd/webhash
curl -s https://host/app.js | sha256sum
```

Compare the hash of each file with the `webhash` output (`index.html` is served at `/`). This detects substitution after the fact but does not prevent it.

## Deploying on Render

The root contains the Blueprint [`render.yaml`](render.yaml): a web service from the `Dockerfile` and PostgreSQL, both on the free plan in the Frankfurt region.

1. In Render: **New → Blueprint**, pick this repository and branch.
2. Render creates the `nullchat-db` database, injects `DATABASE_URL`, generates `IDEMPOTENCY_ENCRYPTION_KEY` itself and sets `MIGRATE_ON_START=true`: the free plan has no pre-deploy step, so the server applies the embedded migrations before starting.
3. After the deploy, check `https://<service>.onrender.com/readyz`. Render provides HTTPS; WebCrypto does not work without HTTPS.

### Automatic deploy from `main`

Every push to `main` deploys to the stand (https://nullchat-xdgz.onrender.com) once the `go-checks` job passes: the `deploy-staging` job in `.gitlab-ci.yml` calls the service's Render Deploy Hook with `ref=$CI_COMMIT_SHA`, so exactly the checked commit is deployed. One-time setup:

1. Render → service → **Settings**: set **Branch** to `main` and **Auto-Deploy** to **Off** (otherwise Render deploys on its own, before the checks).
2. Copy the **Deploy Hook** URL there, `https://api.render.com/deploy/srv-…?key=<KEY>`.
3. GitLab → **Settings → CI/CD → Variables**: add `RENDER_DEPLOY_HOOK_KEY` = `<KEY>`, flags **Protected** and **Masked**. `main` must be a protected branch.

The job only starts the deploy; its progress and result are in the Render dashboard. To roll back, redeploy an earlier deploy in Render or revert the commit in `main`.

Free plan limits: the service sleeps after about 15 minutes without traffic (the WebSocket drops, the client reconnects, rooms and history persist in the database), the first request after sleep takes about a minute, and the free database is deleted after 30 days. There must be exactly one instance: the event hub lives in process memory.

If the logs show `room expiry failed; will retry`, the `cause` field hints at the reason: `sqlstate 42P01` means the migrations were not applied (enable `MIGRATE_ON_START` or run `/migrate`), `connect` or `connection` means the database is unreachable.

## Checks

```sh
bash scripts/run_test.sh
go test -race ./...
go vet ./...
go build ./cmd/server ./cmd/migrate
node --check web/app.js
node --test web/crypto.test.mjs
docker compose run --rm test
```

The `test` service from the `test` profile is started explicitly by the command above, uses the Dockerfile target `build` with a C toolchain and runs `go test -tags=integration -race ./internal/storage/postgres` against a separate `nullchat_test`. The tests apply the migrations themselves and wipe the tables: never point `TEST_DATABASE_URL` at a production database and do not run two test suites at once against the same database. Without `TEST_DATABASE_URL`, checks that need a database are skipped even with the `integration` tag.

The tests cover the crypto module (identical keys and codes on both sides, rejection on key or AAD substitution, non-extractability, ciphertext sizes matching `envelope.go`), `join_proof` verification and invite revocation, security headers, concurrent join, deduplication, rollback, TTL, the snapshot/subscribe boundary, migrations, commit-before-ack and recovery after a process restart. They need access to local TCP ports; `-race` needs a supported platform and a C toolchain. Exact results and limits: [VERIFICATION.md](docs/VERIFICATION.md).

Manual scenario:

1. Create a room, open the invite in another window and join.
2. Send messages both ways, reload one tab and check the history.
3. Open the invite again: a third participant must not get in.
4. Disconnect the network, restore it and check that the connection recovers.
   Then run `docker compose restart nullchat`: before the TTL, both tabs must restore their sessions and the acknowledged history. Check `curl --fail http://127.0.0.1:8080/readyz`.
5. End the chat: both participants lose access, the history disappears from the interface.
6. Create a one-minute room and check that it closes automatically.

## Project layout

```text
cmd/server/       configuration, dependency wiring, startup and shutdown
cmd/migrate/      explicit run of the embedded SQL migrations
cmd/webhash/      hashes of the embedded frontend for verifying a deployment
internal/chat/    models, validation and the Application port
internal/config/  environment validation without printing secrets
internal/storage/postgres/ transactions, persistence, migrations and integration tests
internal/storage/memory/   adapter for demo/tests, not the server runtime
internal/realtime/ bounded in-process event hub
internal/httpapi/ HTTP, WebSocket, authentication and transport limits
web/              HTML, CSS and JavaScript (crypto.js is E2EE, keystore.js keeps keys in IndexedDB), embedded via go:embed
docs/             architecture decisions, the plan and an archive of the original spec
```

Application logic does not import HTTP or WebSocket. The transport uses `chat.Application` through `httpapi.Chat`; the PostgreSQL adapter owns the atomic scenarios, SQL and transactions. The runtime is wired in `cmd/server`. It is a single monolith; a gateway/core split remains a separate future stage.

- [Current spec](SPEC.md)
- [Step-by-step modernization plan: PostgreSQL, reliability and two services](docs/PLAN.md)
- [Architecture and a possible two-service split](docs/ARCHITECTURE.md)
- [The original Matrix proposal (archive)](docs/archive/matrix-gateway-draft.md)
