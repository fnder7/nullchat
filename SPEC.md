# Specification: nullchat

Status: the PostgreSQL foundation of version 0.2 is implemented; outbox/replay, paging and multiple services are deferred. Updated 2026-11-06 (end-to-end encryption). This document replaces the original spec of the Matrix gateway. The checks performed and their limits are listed in [VERIFICATION.md](docs/VERIFICATION.md); the full 0.2 roadmap is not finished yet.

## 1. Goal

Build a temporary chat without sign-up as a Go portfolio project. A user creates a room for two, passes on an invite and talks until the TTL expires. Demonstrate concurrency, real network connections, explicit delivery guarantees, testing and a clear architecture.

Matrix, federation, accounts, files, calls, groups, typing and read receipts are not part of the demo. Messages and the room title are encrypted in the browser (E2EE, Decision 006 in the [architecture](docs/ARCHITECTURE.md), details in the [design](docs/superpowers/specs/2026-11-06-e2ee-design.md)). Two microservices are considered after a working version with PostgreSQL.

## 2. User scenario

1. The creator enters an optional title (up to 60 Unicode characters), a TTL of 1–60 minutes (30 by default) and, if desired, a password. The browser creates a link secret and an ephemeral ECDH pair, encrypts the title and sends the server only the ciphertext, the signed public key and the join verifier.
2. The server creates the room, the creator's session and a one-time guest invite token. For each participant the backend generates a random English display name; the name is unique within the room, and internal identifiers are random.
3. The invite looks like `https://host/#invite=<token>&k=<secret>[&pw=1]`. The fragment is not sent in the page's HTTP request, so the server does not see the secret `k`. `pw=1` shows the password field. Opening the page does not take the seat: the guest presses "Join the room".
4. The guest sends a `join_proof` computed from the link secret and the password. The server compares it with the verifier: on a mismatch it returns `join_proof_invalid` and does not consume the invite; after 5 failures the invite is revoked (`invite_revoked`). A successful join atomically consumes the invite and issues a separate session. Concurrent joins cannot create a third participant.
5. Once the guest joins, both browsers verify the signatures of the public keys, derive the shared message key and show the same safety code. Writing is not possible before that: there is no shared key yet. Both participants receive the history and new messages over WebSocket.
6. Reloading the page keeps the session within the tab. On a network drop the interface reconnects with a delay of 1–10 seconds.
7. Either participant can end the chat for both. On TTL the same happens automatically.

## 3. Lifecycle

- The TTL starts at creation, is not extended by activity and is checked by the server.
- After `expires_at`, join, subscribe and send are forbidden regardless of the background cleaner.
- The cleaner checks rooms once per second. An online client receives `room_expired`; delivery of the notification depends on the network.
- `room_closed` means an explicit end by either participant.
- The room's sessions and invites are revoked and the history becomes unavailable through the API; the expiry worker deletes expired idempotency rows, while the other PostgreSQL rows remain until a separately implemented physical cleanup.
- Closing a tab is not a reliable leave signal and does not free the guest's seat. A session is valid until the room closes.
- In the current runtime with PostgreSQL, a backend restart breaks WebSocket connections but does not close an active room or revoke valid sessions. The client can reconnect with the same session until `expires_at` or an explicit end of the room.
- Stopping the application and ending a conversation are different operations: graceful shutdown does not send `room_closed` and does not delete the room from persistent storage.

## 4. HTTP API v1

Responses are JSON, errors look like `{"error":"code"}`. All responses carry `Cache-Control: no-store, no-transform`.

| Method and path | Request | Result |
|---|---|---|
| `POST /api/v1/rooms` | `{"encrypted_title":"…", "ttl_minutes":30, "key_share":{"public_key":"…","mac":"…"}, "join_verifier":"…"}` | `201`, room, self, session_token, invite_token |
| `POST /api/v1/join` | `{"invite_token":"…", "join_proof":"…", "key_share":{…}}` | `200`, room, self, session_token |
| `POST /api/v1/end` | Header `Authorization: Bearer <session_token>` | `200`, `{"ok":true}`; closes the room |
| `GET /ws` | WebSocket upgrade, then authenticate | Event stream |
| `GET /healthz` | — | `200`, `{"status":"ok"}` |
| `GET /readyz` | — | `200`, `{"status":"ready"}` on a successful database ping; otherwise `503 database_unavailable` |

`room` contains `id`, `encrypted_title`, `expires_at`, `participants`, `messages`. A participant (`self` and the elements of `participants`) contains `id`, `name`, `role` (`creator`/`guest`) and `key_share`.

Binary fields are base64url without `=` (strict encoding). The server checks only the shape: `encrypted_title` is exactly 284 bytes; `key_share.public_key` is 65 bytes starting with `0x04`; `key_share.mac`, `join_verifier`, `join_proof` are 32 bytes; `ciphertext` is 12 + 16 + n bytes, where n is a multiple of 256 and 256 ≤ n ≤ 4096.

Create/join accept an optional `Idempotency-Key`: 32–128 printable ASCII characters. The client generates a random key and keeps it to retry the same request. The fingerprint covers the endpoint and all request fields, including the title ciphertext, the key share and the verifier/proof. So the client encrypts the request once and on retry sends it unchanged. The replayed credentials are encrypted with AES-GCM; the same key with a different endpoint/fingerprint gets `409 idempotency_key_conflict`. After end/TTL a replay gets `410 room_unavailable`.

`/healthz` does not touch the database. `/readyz` checks a ping, not the schema version: migrations run separately (`cmd/migrate`) or at startup with `MIGRATE_ON_START=true` for hosts without a pre-deploy step.

Main errors: `invalid_input` (400), `session_invalid` (401), `origin_forbidden` and `join_proof_invalid` (403), `room_unavailable` (410), `message_id_conflict` and `idempotency_key_conflict` (409), `rate_limited` and `capacity_reached` (429), `database_unavailable` (503). Unavailable/used invites do not reveal room details.

Limits differ by cause: `room_full` means the room already has two participants; `message_limit` means the room's message limit is reached; `connection_limit` means the WebSocket/subscription limit is exhausted; `rate_limited` means the request or command rate was exceeded; `database_unavailable` means storage is not ready. The client must be able to tell what can be retried and what needs a different action.

## 5. WebSocket protocol

First, within five seconds after the upgrade:

```json
{"type":"authenticate","session_token":"<64 hex characters>"}
```

The session token is not passed in the URL. After authentication the server sends a `snapshot` with `payload: {room, self}`. The history snapshot and the subscription to the stream happen atomically.

Sending:

```json
{"type":"send_message","client_message_id":"<UUID>","ciphertext":"<base64url>"}
```

Server message:

```json
{
  "type": "message",
  "payload": {
    "sequence": 1,
    "id": "<server ID>",
    "client_message_id": "<UUID>",
    "sender_id": "<participant ID>",
    "ciphertext": "<base64url>",
    "created_at": "2026-09-20T12:00:00Z"
  }
}
```

| Event | Payload / purpose |
|---|---|
| `snapshot` | `{room, self}`: the state after connecting |
| `message` | A message; both participants receive it |
| `ack` | The same message: receipt acknowledgement to the sender |
| `participants` | Array of participants with key shares after the guest joins; not an online indicator |
| `invite_revoked` | `{}`: the invite was revoked after 5 wrong `join_proof` attempts |
| `error` | `{code, client_message_id?}` |
| `room_expired` | `{}`: the TTL expired |
| `room_closed` | `{}`: a participant ended the chat |
| `server_shutdown` | `{}`: the server is stopping; sent before close `1001`, the browser keeps the session and reconnects |

### Implemented guarantees

- `ack` is returned only after a successful commit of the message to PostgreSQL. It does not confirm reading or delivery to the contact.
- Repeating `(participant_id, client_message_id)` with the same ciphertext returns the earlier result. With a different ciphertext, `message_id_conflict`. The client retries sending with the same ciphertext rather than re-encrypting.
- Each new message gets an increasing `sequence` within the room. The UI sorts by it and drops duplicates by `id`; `ack` and `message` may arrive in either order.
- On reconnect, the whole room history replaces the local snapshot. Unacknowledged messages of the open page are resent with the same IDs.
- The client trims the text at both ends; it must not be empty and must take at most 4000 bytes of UTF-8. The plaintext `0x01 ‖ counter (uint32) ‖ prev (uint32) ‖ text` is padded to a multiple of 256 bytes and encrypted with AES-256-GCM; the AAD includes the room, the sender and `client_message_id`. `counter` grows in send order, `prev` is the sender's last own message already accepted by the server. The recipient hides a replay or reorder with a warning and flags a message if a previously accepted one is missing. A failed send is re-encrypted on "Retry" with a new `client_message_id`.
- Exactly-once over the network is not claimed. Rooms, sessions and acknowledged history survive a backend restart as long as the database is intact. The event hub stays in memory: there is no durable outbox/replay. A failure after commit but before broadcast is recovered by the snapshot on reconnect; a client that stays connected may miss the event.

### Implemented PostgreSQL foundation of version 0.2

These properties are covered by the PostgreSQL integration tests: `TestOperationsCommitFailureDoesNotPublish`, `TestServerRestartPreservesCommittedRoom`, `TestCommittedRoomSurvivesServiceRestart`, `TestConcurrentIdempotencyFirstUse`, `TestExpireDeletesExpiredIdempotencyAndAllowsKeyReuse`, the create/join replay/conflict tests and the access revocation tests. They do not mean the outbox, paging, full history cleanup or all roadmap UI states are done.

- `ack` means the message was accepted and successfully committed by PostgreSQL (`commit`). It does not confirm delivery to the contact or reading. Actual durability depends on the PostgreSQL settings, including the write acknowledgement mode and the disk/replication behavior.
- Retrying a send with the same `(room_id, participant_id, client_message_id)` and the same ciphertext returns the originally accepted message. A different payload with the same key gets `message_id_conflict`.
- After a lost HTTP response, retrying `create` or `join` with the same idempotency key and the same request fingerprint returns the original result within the retry window. A different request with the same key gets `idempotency_key_conflict`. After the window ends, a retry is not guaranteed.
- Losing the WebSocket or a backend restart does not close the room or revoke the session. After recovery the client gets the current state and continues with the same token, if the TTL has not expired and no participant ended the room.
- On TTL expiry or an explicit end, access is revoked permanently: late sends and reconnects get `session_invalid` or `room_unavailable` according to the HTTP/WebSocket contract; a valid token does not restore access.

## 6. Interface states (target full contract)

Keeping the session on `server_shutdown` and on retryable temporary errors (including a WS close with code `1013`) is implemented. A full redesign of failed/pending and a separate unavailable display is still deferred; this section is not a report that all UI requirements are done.

The client explicitly shows one of the following states:

- `connecting`: the first WebSocket connection is being established;
- `connected`: the session is confirmed, the event stream is active;
- `reconnecting`: a temporary network or server failure, the session is kept and bounded retries run;
- `expired`: the TTL expired, access is revoked permanently;
- `closed`: a participant ended the room, access is revoked permanently;
- `unavailable`: the server or the database is temporarily unavailable, a retry is possible without deleting the valid session.

`reconnecting` and `unavailable` do not mean the session expired. `expired`, `closed` and an invalid token mean permanent loss of access. Unsent text and a failed message must have their own error state and must not disappear from the interface automatically.

## 7. Limits and privacy

A summary of observers and what each one sees is in [SECURITY.md](docs/SECURITY.md).

- Tokens: 32 cryptographically random bytes; server indexes store SHA-256 of the tokens. In `idempotency_requests` the credentials exist only inside the AES-GCM encrypted response; the deployment key is kept outside the repository and preserved across restarts.
- `sessionStorage` holds the session token, the public key, the message counter and (for the creator until the guest joins) the link secret; the history, the password and private keys are not stored there. The private ECDH key and the derived keys are non-extractable `CryptoKey`s in IndexedDB; the record is deleted when the room ends, and expired records are purged when the page opens.
- WebSocket and the API check Origin against the host. Requests without Origin are allowed for non-browser clients; Origin does not replace authentication.
- The application itself never compresses WebSocket (`permessage-deflate`) or HTTP responses (explicitly disabled) and sends `Cache-Control: no-store, no-transform`. Render's edge nevertheless ignores `no-transform` and applies brotli to HTTP responses, including the create/join JSON with tokens (observed on 2026-11-07). BREACH still does not apply: every such response carries freshly generated tokens, so no secret repeats across responses, and credentials are not ambient (no cookies; the tokens are sent by the page's own JS). WebSocket traffic is not compressed. The `net/http` error log passes through a filter that replaces client addresses with `<addr>`.
- CSP with Trusted Types (`require-trusted-types-for 'script'; trusted-types 'none'`), `textContent` instead of inserting message HTML, no external scripts, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy` and `Cross-Origin-Resource-Policy: same-origin`, a `Permissions-Policy` without camera, microphone and geolocation. `go run ./cmd/webhash` prints the SHA-256 of the embedded files for verifying the deployed frontend.
- Implemented limits: 100 active rooms, 500 messages per room; 400 sockets in total and 2 per session for a short overlap on reconnect. Create, join and socket upgrade each have their own admission budget.
- Storage today: end/TTL change the status and revoke access; the expiry worker deletes `idempotency_requests` after their own `expires_at`, allowing the key to be reused as a new one. Physical cleanup of the other room rows is not implemented; they contain only ciphertexts, key shares and the SHA-256 of the verifier. Migration `0003_e2ee` closed rooms created before E2EE and deleted their plaintext messages and titles. The history retention target of room TTL plus 24 hours remains deferred, not a deletion guarantee. There is no durable event store. WAL/backup policies and deletion from the main database need a separate implementation; instant physical erasure is not promised.
- The `create`/`join` result for an idempotent retry is available until the room ends, but no more than 24 hours after the original request; after that a retry is not guaranteed. Plain session/invite tokens are not stored in logs.
- Admission budgets per process, each in its own one-minute window: 30 room creations, 60 joins, 480 socket upgrades. They are independent, so a flood of room creation cannot block recovery of existing sessions; an exhausted budget answers `rate_limited` with `Retry-After`. The budgets are global rather than per client, so one client can still exhaust a single kind of work for everyone. New messages from one session: at most one per 200 ms. Shared limits do not replace abuse protection for a public service.
- The subscriber queue is 64 events; an overflowing subscriber is detached from the stream, then the socket is closed and the client recovers the history. Network writes have a 5-second timeout.
- Ping every 25 seconds, a 5-second response timeout; application WebSocket frames are at most 8192 bytes.
- Application logs contain startup, shutdown and system errors. Texts, tokens, IPs and User-Agent are not written by application code; the standard HTTP server may log crash errors. A public deployment requires a separate review of the proxy/runtime logs.
- HTTPS is mandatory outside localhost (WebCrypto works only in a secure context). The backend does not see the message text or the title: only ciphertext, size to within 256 bytes, times, participants and public keys. The server cannot substitute keys without the link secret (and the password): a substitution is detected by the signature check and the safety code. Limitations: web E2EE trusts the JavaScript the server serves, so a compromised server can serve modified code; whoever intercepted a link without a password can join in place of the guest (the guest will see that the invite was used); the client does not detect truncation of the history tail. Full anonymity or physical memory erasure is not promised.

## 8. Acceptance criteria for the current monolith

- Runs per the README with PostgreSQL 17, an explicit migration and an environment secret; without Matrix or a frontend toolchain. Go 1.25+ for building on the host.
- Two independent browser contexts exchange messages.
- One-time join and the two-participant limit withstand concurrent requests.
- A repeated message does not duplicate the history.
- Reload/reconnect restores the current room and history.
- TTL and an explicit end revoke both sessions.
- A slow subscriber does not block the others.
- The server never receives the plaintext of messages or the title; the safety codes of both participants match; a wrong password does not take the seat.
- `go test -race ./...`, `go vet ./...` and `node --test web/crypto.test.mjs` pass.
- The README honestly describes the demo's limits and how to run it.

## 9. Acceptance criteria for version 0.2

- A backend restart does not close an active room: after the service recovers, both participants can reconnect with their sessions before the TTL.
- `ack` is issued only after the message is committed to PostgreSQL; an artificial failure before commit leaves no message, and a failure after commit allows a safe retry with the same `client_message_id`.
- Temporary network/database unavailability does not move a valid session into a permanently revoked state.
- A full room, the message limit, the connection limit, a too-frequent request and an unavailable database return different stable codes.
- Retrying create/join after a lost response does not create a second room or participant within the agreed idempotency window.

## 10. Next version

Deferred: transactional outbox and durable event replay, cursors and paged history, physical cleanup/retention, the full failed/pending UI, observability, CI and load testing. Multiple core instances and a gateway/core split need a separate design; one backend is supported. Stage details and criteria are in the [plan](docs/PLAN.md).
