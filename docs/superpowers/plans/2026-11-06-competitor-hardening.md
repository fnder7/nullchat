# Modernization plan based on the competitor's decisions (chrmontessori/nullchat)

> **For the implementer:** this is a plan of stages, not a step-by-step TDD plan. Before implementing each stage, a separate detailed plan is written following [`2026-11-06-e2ee.md`](2026-11-06-e2ee.md): failing test → implementation → green test → commit. Items marked **Decision** require the project owner's choice before work starts.

**Source.** The public repository [chrmontessori/nullchat](https://github.com/chrmontessori/nullchat) (Next.js + Node `ws`, NaCl secretbox, Argon2id), read on 2026-11-06 without running the code. References like `server/room.ts:30` point to files of that repository. No code is copied: the competitor has no LICENSE file (the README says MIT, `package.json` says ISC), so only ideas are taken.

**nullchat's starting point (`dev`, 3d3dd4d).** E2EE in the browser (ECDH P-256 + link secret + optional password, AES-256-GCM, 256-byte blocks, counters, safety code), non-extractable keys in IndexedDB, CSP with Trusted Types, COOP/CORP, a frontend hash manifest, PostgreSQL, deploy on Render Free.

## 1. Summary: what we take, adapt and reject

| Competitor's decision | Where they have it | What we have now | Verdict |
|---|---|---|---|
| Fixed ciphertext size (16 KiB, noise, length prefix) | `lib/crypto.ts:45,143-159` | 256-byte blocks: length is visible to within a block | **Take**, stage B |
| Zeroing raw key material (`fill(0)`) | `lib/crypto.ts:86,111-119` | Bytes of the secret, password, HKDF and ECDH stay in memory until GC | **Take**, stage A |
| Disabled WS compression (`perMessageDeflate:false`) against CRIME/BREACH | `server/index.ts:180` | Disabled only by default in `coder/websocket`, not pinned | **Pin it**, stage A |
| Not passing the IP to the application (`X-Forwarded-For ""`), logs to `/dev/null` | `deploy/nginx/nullchat-ws.conf:24`, `deploy/nullchat.service:71` | Application logs without IPs, but the standard `http.Server` writes `panic serving <IP:port>` | **Adapt**, stage A |
| Deleting messages: after 24 h of storage and 5 min after being read | `server/room.ts:24-26,156-174` | No physical cleanup, rows of closed rooms live forever | **Take the cleanup**, stage C. Burn-after-reading is a **Decision**, stage C2 |
| Panic button (Esc×3: wipe the DOM, storage, clipboard, leave the page) | `components/ChatRoom.tsx:332-374` | None | **Adapt**, stage D |
| Auto-clearing the clipboard 15 s after copying | `components/ChatRoom.tsx:311-321` | None | **Adapt** (**Decision** on behavior), stage D |
| Inactivity lock | `components/ChatRoom.tsx:272-309` | None | **Decision**, stage D3 |
| Reproducible build (fixed buildId) and SRI in the Tor build | `scripts/add-sri.js` | The `cmd/webhash` manifest exists, the build is not reproducible | **Take reproducibility**, stage E. SRI is optional |
| Limits: 5 connections and 10 requests per minute per IP (nginx), 1 message/s, room flood limit | `deploy/nginx/nullchat-ws.conf:7-15` | One shared budget of 120/min per process, 200 ms per session | **Adapt** without storing IPs, stage F |
| Cover traffic and server padding frames | `server/room.ts:295-301`, flag `c:1` in plaintext | None | **Reject**: the server sees the flag, and indistinguishable cover eats the 500-message limit. Revisit after stage B |
| Password → `roomId` via Argon2id with a global salt; `roomId` in the WebSocket URL | `lib/room.ts:12`, `ChatRoom.tsx:40` | 32 random bytes in the fragment, the password only adds to them | **Reject**: lets the server brute-force passwords offline across all rooms at once |
| Argon2id (hash-wasm) | `lib/crypto.ts:9` | PBKDF2-SHA256 600,000 iterations (WebCrypto) | **Reject**: needs wasm and `'wasm-unsafe-eval'` in the CSP; our password protects only together with the link secret |
| `'unsafe-inline'` in `script-src` | `server/index.ts:41-74` | `script-src 'self'` + Trusted Types | **Reject**, ours is already better |
| Up to 50 participants, presence without a list | `server/room.ts` | Exactly two, a safety code | **Reject**: a third participant joins unnoticed |
| TOFU verifier without a failure counter | `server/room.ts:233-247` | The creator sets the verifier, 5 failures burn the invite | **Reject**, ours is already better |
| Tor onion, systemd sandbox, disabled swap, tmpfs storage | `tor/torrc`, `deploy/harden.sh` | Render Free: we do not control the host | **Reject for Render**. For self-hosting, in the stage E documentation |

Lessons from the competitor's bugs become regression tests: a command bypassing the limit (a cover frame goes to `relayCover` and is broadcast to everyone before the rate check: `server/room.ts:371-374` versus the limit in `server/room.ts:380-385`), secrets in URLs, `Math.random` for anything security-related.

## 2. Stages in order

Order: first the cheap and safe (A), then what changes the protocol and storage (B, C), then UX features that need agreement (D), and infrastructure (E, F). Effort estimate: S is up to half a day, M is a day or two, L is more.

### Stage A. Quick hardening without protocol changes (S)

**Status:** done on 2026-11-07 in the `feature/hardening-a` branch (A1–A4).

**A1. Logs without IP addresses.**
- `cmd/server/main.go`: set `http.Server.ErrorLog` to a `log.Logger` over a writer that replaces `IPv4:port`, `[IPv6]:port` with `<addr>` and sends the line to `slog` at WARN level.
- Test: a handler that calls `panic`, plus a request through `httptest`. The line is in the log, the client address is not in it.
- Link: closes the `PLAN.md` step 9 item "no tokens… or personal data in application logs".

**A2. Pin the absence of compression.**
- `internal/httpapi/server.go`: explicit `websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled}`. Do not add gzip middleware for HTTP.
- Test: a WS handshake with `Sec-WebSocket-Extensions: permessage-deflate` does not get this extension in the response. HTTP responses have no `Content-Encoding`.

**A3. Zeroing key material in `web/crypto.js`.**
- After `importKey` and `deriveBits`, call `.fill(0)` on the buffers the module owns: the secret bytes, `pw`, `IKM`, HKDF outputs, the ECDH shared secret, the plaintext after `encrypt`.
- In `app.js`, on `finish()`, zero the reachable `Uint8Array`s and drop the references (this is already done for `CryptoKey`).
- Be honest in the documentation: JS strings and copies inside the engine are not wiped, this is best effort.
- Test (node): a `withWiped(bytes, fn)` wrapper leaves the buffer zeroed after the call, including when `fn` throws.

**A4. "What the server sees" document.**
- `docs/SECURITY.md`: a table of observers (the application server, the database, Render and proxy logs, a network observer behind TLS, a link holder, a holder of the link and the password, compromised JS) and what each of them sees or can do.
- Also links to the threat model in the E2EE design and a "Why we differ from the namesake project" section with the "Reject" rows from the table above.

**Done when** the A1–A3 tests are green and README and SPEC link to `SECURITY.md`.

### Stage B. Fixed message size (M)

**Goal:** the server and a network observer cannot tell a short message from a long one.

- **Decision B0.** Choose one of:
  1. **A single size of 4096 bytes of plaintext (recommended).** Every message is 4124 bytes, about 5.5 KB in base64. Worst case by volume: 100 rooms × 500 messages ≈ 275 MB. This fits in Render's free Postgres (1 GB), but together with stage C the volume stays low.
  2. **Three classes: 256 / 1024 / 4096.** Cheaper by volume, but three distinguishable length classes remain.
- `web/crypto.js`: `pad()` always to the chosen size. `MAX_TEXT_BYTES` does not change.
- `internal/chat/envelope.go`: accept only the chosen size (or classes). For one release keep accepting the old 256-byte blocks for rooms opened during the deploy (they live at most 60 minutes); remove this in the second release.
- The title is already fixed-size (284 bytes); leave it alone.
- Tests: the node size test reads the new constant from `envelope.go`. Go test: a message of the "old" size is rejected after the transition release.
- Documentation: SPEC §4–5, E2EE design §5.

**Done when** all ciphertexts have the same length (check in the browser on Render: the messages "a" and 4000 bytes give the same `ciphertext` size).

### Stage C. Nothing is kept longer than needed (M)

**C1. Physical cleanup of closed rooms.**
- A new background step in `Expire` or a separate ticker: delete rows of rooms in status `closed`/`expired` whose `closed_at` is older than `PURGE_AFTER` (10 minutes by default, configurable). Delete in batches of 100: `messages` → `sessions` → `invites` → `participants` → `idempotency_requests` → `rooms`. Locks in the order agreed with `closeRoomTx`.
- Migration `0004`: an index on `(status, closed_at)` and, if needed, `ON DELETE CASCADE` instead of the manual order.
- `memory`: already frees the data. Make sure `endRoom` leaves no references.
- Tests (integration): a closed room disappears entirely after `PURGE_AFTER`, an open one is untouched, retrying create/join after cleanup gets `room_unavailable` rather than a replayed response. Concurrent `End` and cleanup without deadlocks.
- Documentation: rewrite SPEC §7 "Storage" from "not implemented" to a concrete period. Note that Render's WAL and backups follow Render's policy.
- Link: closes the "physical cleanup" from the `PLAN.md` status and the "TTL + 24 hours" retention target, and the period becomes shorter.

**C2. Disappearing messages (Decision).** The competitor burns a message 5 minutes after it is read, but that needs read receipts, which SPEC §1 excludes. An alternative without read receipts:
- the creator chooses "messages live N minutes" (off / 5 / 15);
- the period is encrypted in the title or passed as a plaintext `message_ttl` field;
- the server deletes messages older than N minutes with the same cleaner, and clients remove them from the DOM by `created_at`.

A separate design document only if the project owner wants this.

### Stage D. Traces on the participant's device (S–M, decisions needed)

**D1. Panic button.**
- A triple Esc press (within 1 s) plus a visible button in the sidebar for mobile.
- Action: delete the key record in IndexedDB and the tab's `sessionStorage`, clear the DOM, zero the keys (A3), `history.replaceState` to `/`, go to an empty home page. Handle the return from bfcache (`pageshow` with `persisted`).
- **Decision:** local cleanup only (the contact stays in the room, as with the competitor) or local cleanup plus `End` for both.
- Test: a contract test in `app_test.go` for the handler and a manual browser check: after panic IndexedDB is empty, "back" does not show the conversation.

**D2. Clipboard.**
- After "Copy link", overwrite the clipboard with an empty string after 60 seconds and show the hint "The link will be removed from the clipboard in 60 s". Also on panic and on `finish()`.
- **Decision:** clear blindly (like the competitor: may erase something the user copied later) or only via an explicit "Clear clipboard" button.

**D3. Inactivity lock (Decision).** The keys are in IndexedDB, so a "lock" without deleting the keys is cosmetic, and with deletion it equals panic. Options: off (default) / panic after N minutes of inactivity with a 30 s warning.

### Stage E. Verifiability of the deployed code (M)

- `Dockerfile`: pin base images by digest, `-trimpath -buildvcs=false`, `SOURCE_DATE_EPOCH` from the commit time.
- CI (`.gitlab-ci.yml`): a `reproducible` job: two builds, compare the binary's SHA-256. A `webhash.txt` artifact (output of `go run ./cmd/webhash`) for every tag.
- GitLab release: the manifest hash in the description.
- README "Frontend verification": compare against the release manifest, not only against a local build.
- **Optional (S):** SRI for `app.js` and `style.css` in `index.html` plus an import map with `integrity` for `crypto.js`/`keystore.js`. Protects against substitution along the path (CDN, proxy), but not against the server itself. Regenerating the attributes is a build step.
- For self-hosting (not Render): a documentation section with the competitor's ideas: systemd sandbox, disabled swap, `LimitCORE=0`, reverse proxy logs without IPs.

### Stage F. Limiting abuse without storing IPs (M)

The lesson from the competitor's bug: every incoming command goes through the limit **before** any work.

- `internal/httpapi`: a per-connection budget for all WS frames, including malformed JSON and unknown types. When exceeded, close with `rate_limited`. Test: a stream of garbage frames closes the connection and creates no work in `chat`.
- Separate create / join / upgrade budgets (`PLAN.md` step 9 item): recovering a valid session does not share a quota with room creation.
- A per-client limit without storing IPs: an in-memory token bucket keyed by `HMAC(process key, X-Forwarded-For from Render)`. The key changes on restart, entries live at most 10 minutes and are never logged. `SECURITY.md` must state honestly that the IP is processed briefly. Trust `X-Forwarded-For` only with `TRUST_PROXY=render`.
- Tests: two "clients" with different forwarded addresses do not share a budget. Without `TRUST_PROXY` the header is ignored.

## 3. Order and relation to the main plan

| Queue | Stage | Dependencies | Overlap with `docs/PLAN.md` |
|---|---|---|---|
| 1 | A1–A4 | — | Step 9 (logs), step 12 (documentation) |
| 2 | C1 | — | Status "physical cleanup deferred", retention |
| 3 | B | Decision B0 | — |
| 4 | F | — | Step 9 (budgets, commands, IP) |
| 5 | E | — | Step 11 (CI), step 12 (publication) |
| 6 | D1–D3, C2 | Decisions D1, D2, D3, C2 | — |

After each stage: update `VERIFICATION.md` (what was checked and where), run GitLab CI with PostgreSQL and check in the browser on Render.

## 4. Decisions needed before starting

1. **B0:** a single size of 4096 or three classes 256/1024/4096.
2. **C1:** the `PURGE_AFTER` period after a room closes (proposal: 10 minutes).
3. **C2:** whether disappearing messages are wanted and in what form.
4. **D1:** panic only on one's own side or ending the room for both.
5. **D2:** clipboard clearing automatically after 60 s or only via a button.
6. **D3:** inactivity lock off or panic after N minutes.
7. **F:** whether brief IP processing (HMAC, in memory only, up to 10 minutes) is acceptable for per-client limits.
