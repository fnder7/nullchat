# Security: what each party sees

A short answer to "who can learn what about a conversation". The protocol and its rationale are in the [E2EE design](superpowers/specs/2026-11-06-e2ee-design.md), Decision 006 is in the [architecture](ARCHITECTURE.md).

## Observers

| Who | Sees | Does not see | Can do |
|---|---|---|---|
| **Application server** (nullchat code in process memory) | Ciphertexts of messages and the title, public ECDH keys and their HMACs, SHA-256 of the join verifier, message times, `sequence`, participant roles and random names, the fact of joining and leaving, message size to within a 256-byte block | Text, title, password, link secret, message key | Fail to deliver, delay or delete a message (the client detects replay and reordering, but not truncation of the tail). Serve modified JavaScript: **the main limitation of web E2EE** |
| **Database** and its backups | The same as the server, plus SHA-256 of session and invite tokens, idempotency responses encrypted with AES-GCM (deployment key) | Plain tokens, text, keys | Rows of closed rooms are not physically deleted yet ([plan, stage C1](superpowers/plans/2026-11-06-competitor-hardening.md)) |
| **Application logs** | Startup, shutdown, system errors, database error class (`sqlstate 42P01`) | Tokens, texts, IPs: addresses in the `net/http` error log are replaced with `<addr>` | — |
| **Hosting and its proxy** (Render) | Client IP, request times and volume, TLS termination, its own platform logs | Message contents (ciphertext, and inside TLS) | This is outside the application's control; when self-hosting, reverse proxy logs must be configured separately |
| **Network observer** before TLS | That the client talks to the service, times and sizes of TLS records | All contents | The application itself never compresses WebSocket or HTTP and sends `Cache-Control: no-store, no-transform` on everything except the static font files, which carry no tokens and are cached. Render's edge nevertheless ignores `no-transform` and applies brotli to HTTP responses, including the create/join JSON with tokens (observed on 2026-11-07). BREACH still does not apply: every such response carries freshly generated tokens, so no secret repeats across responses, and credentials are not ambient (no cookies; the tokens are sent by the page's own JS). WebSocket traffic is not compressed |
| **Holder of the link without the password** (if a password is set) | Nothing: without the password the server does not let them in (`join_proof_invalid`), after 5 failures the invite is revoked | — | — |
| **Holder of the link** (no password) or **of the link and the password** | Can join in place of the guest while the invite is unused. The real guest will see "invite already used"; the creator will detect the substitution by comparing the code by voice with the real contact | Messages of a conversation that has already ended: the key is derived from ephemeral ECDH | — |
| **Script injected into the page** (XSS, extension) | The decrypted conversation on screen; can use the keys while the tab is open | The private ECDH key and derived keys: they are non-extractable (`extractable: false`) | Trusted Types without policies blocks `innerHTML`, `eval` and string handlers; there are no external scripts |
| **Someone who obtains a participant's device** | Keys in IndexedDB until the room ends or until the next page open; the token and counter in the tab's `sessionStorage` | The password (not stored) | The conversation can be recovered only while the room is open on the server |

## Client-side hygiene

- Byte buffers are zeroed right after being passed to WebCrypto: the password, the bytes of the PBKDF2 derivation, the decoded link secret, raw HKDF outputs, the ECDH shared secret, the plaintext of a message and of the title after encryption and decryption.
- JavaScript strings cannot be wiped. So until a successful create/join, the page memory keeps the password in the input field and the base64 string of its derivation in the prepared request; for the creator until the guest joins, the link secret in the tab state; decrypted texts in the DOM while the conversation is open. This is best effort, not a wiping guarantee.
- After the guest joins, the link secret is also deleted from the creator's `sessionStorage`.
- When the room ends, the key record in IndexedDB is deleted and `roomSalt` is zeroed.

## Verifying the deployed code

`go run ./cmd/webhash` prints the SHA-256 of each embedded frontend file; comparing it with `curl -s https://host/<file> | sha256sum` shows that the server serves the same code as the repository (`index.html` is at `/`). This detects substitution after the fact but does not prevent it.

## Why we differ from the namesake project

An analysis of the public project with the same name is in the [hardening plan](superpowers/plans/2026-11-06-competitor-hardening.md). We deliberately do not repeat:

- **A room from a password alone with a shared salt.** The room ID derived from the password goes to the server, and the server can brute-force passwords offline across all rooms at once. We have 32 random bytes in the link fragment; the password only adds to them.
- **Argon2id via WebAssembly.** It requires `'wasm-unsafe-eval'` in the CSP. Our password protects only together with the link secret, so PBKDF2-SHA256 (600,000 iterations) is enough.
- **`'unsafe-inline'` in `script-src`.** It makes the CSP worthless against XSS.
- **Rooms for many participants without a presence list.** A third participant joins unnoticed. We have exactly two and a safety code.
- **Cover traffic with a plaintext flag.** The server tells it apart from real messages; moreover, in the other project this command bypassed the rate limit. The lesson goes into stage F of the plan: a per-connection budget for all WS frames, including malformed and unknown ones, before any work. Today only message sending has a rate limit, and an unknown command closes the connection.
