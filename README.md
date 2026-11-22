# nullchat

**An end-to-end encrypted, self-destructing chat for two. No sign-up, no accounts, no plaintext on the server.**

**Live demo:** https://nullchat-xdgz.onrender.com
<sub>Hosted on Render's free plan: the first request after a quiet period can take up to a minute while the service wakes up.</sub>

Create a room → send the one-time link → talk until the timer runs out.

## Features

- **End-to-end encryption in the browser.** Messages and the room title are encrypted with AES-256-GCM. The key comes from ephemeral ECDH P-256, authenticated by a secret in the link fragment, which never reaches the server, and an optional password. The server and the database only ever see ciphertext.
- **Key verification.** Both participants see a safety code they can compare out of band to rule out a man-in-the-middle.
- **Password-protected invites.** The server checks a `join_proof` without learning the password, and revokes the invite after 5 failed attempts.
- **One-time invites and TTL rooms.** Rooms live 1–60 minutes. The invite works once, so a third person cannot join. Either participant can end the chat for both.
- **Reliable realtime messaging.** WebSocket with acks issued only after the PostgreSQL commit, deduplication, automatic reconnect and history recovery. Rooms survive backend restarts.
- **Idempotent create/join.** Requests with an `Idempotency-Key` are safe to retry, and the stored replay responses are AES-GCM encrypted.
- **Hardened by default.** Strict CSP with Trusted Types, non-extractable WebCrypto keys in IndexedDB, hashed tokens, no access logs, no compression of secrets.
- **Verifiable frontend.** `cmd/webhash` prints hashes of the embedded web assets, so you can check that a deployment serves exactly the code in this repository.

## Tech stack

| Layer | Technology |
|---|---|
| Backend | Go 1.25, `net/http`, WebSocket, pgx v5 |
| Storage | PostgreSQL 17, embedded SQL migrations with an advisory lock |
| Frontend | Vanilla JS, HTML and CSS embedded with `go:embed`; WebCrypto, IndexedDB; no npm dependencies |
| Infra | Docker (static binaries on a `scratch` image), Docker Compose, Render Blueprint |
| CI/CD | GitLab CI (tests → deploy to Render through a Deploy Hook) and GitHub Actions |

## Architecture

```text
Browser (E2EE: WebCrypto, IndexedDB)
   │  HTTPS + WebSocket, ciphertext only
   ▼
httpapi  ──►  chat.Application (domain, validation)  ──►  storage/postgres
   │                                                       (transactions, migrations)
   └──►  realtime hub (bounded in-process event fan-out)
```

A single Go monolith with clean boundaries: the domain layer does not import HTTP or WebSocket, and the PostgreSQL adapter owns atomic scenarios and transactions. The design and the threat model are in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), [docs/SECURITY.md](docs/SECURITY.md) and the [E2EE design](docs/superpowers/specs/2026-11-06-e2ee-design.md).

## Quick start

Requirements: Docker with Compose and OpenSSL. Ports 5432 and 8080 must be free.

```bash
git clone <this repository>
cd nullchat
./run.sh
```

Open http://127.0.0.1:8080, create a room, and open the invite in a private window.

## Tests

```bash
go test -race ./...
node --test web/crypto.test.mjs
docker compose run --rm test   # PostgreSQL integration tests
```

The tests cover the crypto module (key and AAD substitution, non-extractability), invite revocation, concurrent joins, deduplication, rollback, TTL expiry, migrations, commit-before-ack and recovery after a restart. Details are in [docs/VERIFICATION.md](docs/VERIFICATION.md).

## Documentation

- [Operations guide](docs/OPERATIONS.md): running on the host, configuration, Render deployment, CI/CD, limits
- [Specification](SPEC.md)
- [Security: who sees what](docs/SECURITY.md)
- [Roadmap](docs/PLAN.md)

## Limits

This is a demo deployment: one instance, at most 100 active rooms, 500 messages per room and 400 connections. Web E2EE trusts the JavaScript the server serves; see [SECURITY.md](docs/SECURITY.md) for the full threat model.
