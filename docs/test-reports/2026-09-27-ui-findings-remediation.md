# UI findings remediation acceptance — 2026-09-27

## Scope and baseline

Full P0/P1 matrix from [UI_TEST_CASES.md](../UI_TEST_CASES.md), following
[UI_TESTING_RUNBOOK.md](../UI_TESTING_RUNBOOK.md) and the project
testing-nullchat-ui skill. All 63 cases were directly exercised: **63 PASS,
0 FAIL, 0 BLOCKED**. No cases skipped. Automated results are separate below.

- Tested commit: `4f58e64a4ee90536f01b92e496a84813c5b9e666`
  (`4f58e64`, `fix: pass homepage accessibility audits`).
- Branch/worktree: `fix/ui-findings-remediation`,
  `/home/aidar/projects/nullchat/.worktrees/ui-findings-remediation`.
- Initial `git status --short`: empty; no pre-existing dirty files.
- Started: `2026-09-27T03:07:55+05:00`; final environment check:
  `2026-09-27T12:15:13+05:00` (Asia/Qyzylorda).
- Browser: Chrome DevTools MCP, Chrome `153.0.0.0`, Windows 10 Win64 user agent.
  Desktop widths 1641 and 1440; mobile emulation `390x844`, DPR 1.
- Host tools: `go1.27.1-X:nodwarf5 linux/amd64`, Node `v24.21.0`.
  Compose build uses `golang:1.26-alpine`; database `postgres:17-alpine`.
- Local origin: `http://127.0.0.1:8080`; Compose project explicitly
  `nullchat`, local development database and existing named volume.
- Initial containers: nullchat up 50 minutes, PostgreSQL up 44 minutes
  (healthy), migrate exited 0. Other review-stack containers were untouched.

A fresh development encryption secret was generated without printing or
committing it. Migrations and the application were rebuilt from this worktree:

```text
docker compose up --build --abort-on-container-exit migrate
  migrate-1 exited with code 0
docker compose up --build -d postgres nullchat
  PostgreSQL healthy; migrate exited; nullchat started
curl --fail http://127.0.0.1:8080/readyz
  {"status":"ready"}
```

Initial Compose inspection lacked the required environment variable; the first
Docker access hit sandbox permissions. Both were resolved before testing.
Docker operations and Go cache access used authorized sandbox escalation.

## Browser execution and evidence

Initial isolated contexts: `remediation-creator` (page 9),
`remediation-guest` (11), `remediation-outsider` (10), and
`remediation-random` (12). Creator and guest had independent storage and
different participant identities (Robin and Harbor).

A roughly nine-hour session interruption restarted the browser and discarded
those contexts. Completed observations below are retained from the original
tool transcript. Unfinished TTL checks were restarted with fresh contexts:
`remediation-ttl-creator` (14), `remediation-ttl-guest` (15), and
`remediation-more` (16). P0 messaging/refresh/offline/restart/end checks were
also repeated in these fresh contexts to inspect retained Console/Network
evidence. No expired fixture was treated as a successful interrupted TTL test.

The first room used 30 minutes because the select tool rejected the option
value `15` (it requires visible option text). The 15-minute option was later
selected successfully using the keyboard. All four TTL options were observed.

Browser controls were exercised through clicks, keyboard input and page-side
DOM form submission; snapshots, rendered text, button state and DOM reads
provided UI observations. Page-side fetch was used only for the runbook's
idempotency checks and explicit unauthorized requests. Closing the actual
test WebSocket supplemented Offline emulation; browser clocks were not altered.

### P0

| ID | Result | Direct observation |
|---|---|---|
| UI-01 | PASS | Home HTML, CSS, JS and favicon returned 200; initial Console empty; desktop/mobile screenshots intact. |
| UI-02 | PASS | Named room opened, Robin marked “you”, timer 30:00, 1 / 2, “Connected”. |
| UI-03 | PASS | Copy control exercised; fresh run explicitly showed “Link copied ✓”; independent guest joined, both showed 2 / 2. |
| UI-04 | PASS | Both displayed creator-first then guest-second once each; repeated with fresh-console-guest then fresh-console-creator. |
| UI-05 | PASS | Reloaded both; identities, 2 / 2, confirmed text and connected status restored. |
| UI-06 | PASS | Offline plus actual socket.close showed reconnecting and disabled send; restoring network automatically restored history. |
| UI-07 | PASS | Backend-only restart restored both clients with unchanged messages, participants and expiration. |
| UI-08 | PASS | Guest end made both show “Conversation ended”, zero bubbles, hidden composer and disabled sending; reload/invite denied access. |
| UI-09 | PASS | Fresh P0 Console/Network reviewed through end: only deliberate offline/restart WS errors; successful static/API requests; no unhandled JS errors. |

### Room creation

| ID | Result | Direct observation |
|---|---|---|
| ROOM-01 | PASS | Empty title created “Quiet conversation”. |
| ROOM-02 | PASS | Keyboard entered 60 Cyrillic "Ya" (U+042F) characters; all 60 remained in rendered heading and wrapped. |
| ROOM-03 | PASS | Selected 1, 15, 30, 60 minutes; initial/near-initial timers 01:00, 14:57, 30:00, 60:00. |
| ROOM-04 | PASS | Same create fetch and Idempotency-Key twice: room equality true, session equality true. |
| ROOM-05 | PASS | Stopped PostgreSQL; create returned 503, temporary create-specific error, home stayed visible, storage length 0, create re-enabled. |

### Invitations and participants

| ID | Result | Direct observation |
|---|---|---|
| JOIN-01 | PASS | Valid one-use invite joined same room; both showed 2 / 2. |
| JOIN-02 | PASS | Outsider reused consumed invite: controlled unavailable error, chat hidden, storage length 0. |
| JOIN-03 | PASS | Same join fetch and Idempotency-Key twice: room/session equality true; participants remained 2. |
| JOIN-04 | PASS | invalid-token displayed “This invite is not valid. Check the link or ask for a new one.”; join re-enabled; unknown valid-shaped token controlled. |
| JOIN-05 | PASS | Existing #room in isolated context without storage returned neutral home error; no history/chat. |
| JOIN-06 | PASS | Both reloads retained Robin/Harbor and correct “you” identity. |

### Messages

| ID | Result | Direct observation |
|---|---|---|
| MSG-01 | PASS | Observed pending “Sending…” then confirmed creator-first with time. |
| MSG-02 | PASS | Real Enter key sent keyboard-line-one/newline/line-two. |
| MSG-03 | PASS | Real Shift+Enter inserted newline; count stayed 4 / 500 before Enter. |
| MSG-04 | PASS | Empty and whitespace submissions left count unchanged at 4. |
| MSG-05 | PASS | 4000 ASCII bytes confirmed with no failed entry and appeared at both clients. |
| MSG-06 | PASS | 2001 Cyrillic characters (4002 bytes) produced clear 4000-byte limit error; UI stable. |
| MSG-07 | PASS | <b>HTML</b><script>alert(1)</script> rendered literally; zero b/script children inside bubbles, no script dialog. |
| MSG-08 | PASS | Send adjacent to socket break recovered once; database-failed message retry also confirmed once with no failed copy. |
| MSG-09 | PASS | Confirmed history survived participant reloads and backend restart. |
| MSG-10 | PASS | Three messages per participant at 300ms intervals: both clients showed identical six-message order; failed count 0. |
| MSG-11 | PASS | Local fixture loaded 499 / 500 and 499 articles; UI sent 500th; 501st showed message-limit failure while 500 confirmed messages remained. |

### Connections and recovery

| ID | Result | Direct observation |
|---|---|---|
| NET-01 | PASS | Offline plus closing actual WebSocket showed reconnecting, disabled textarea/send. |
| NET-02 | PASS | Restored network; automatic reconnection returned connected status and both fresh confirmed messages. |
| NET-03 | PASS | Waited for actual 8 attempts: “Server temporarily unavailable”, retry button visible, send disabled. |
| NET-04 | PASS | Restored network and clicked manual retry; connected, existing history recovered, send enabled. |
| NET-05 | PASS | Sent before-disconnect immediately before socket.close; returned once, pending count 0. |
| NET-06 | PASS | Backend-only restarts preserved participants, sessions and confirmed history at both clients. |
| NET-07 | PASS | Database stopped; send became “Not sent” with retry and temporary DB error; confirmed count remained 3. |
| NET-08 | PASS | Database restarted; message retry cleared banner, confirmed count 4, one matching bubble, no failed entries; SQL count exactly 1. |

### Termination and TTL

| ID | Result | Direct observation |
|---|---|---|
| END-01 | PASS | Creator closed 500-message fixture after second participant joined; both saw ended state, guest history 0. |
| END-02 | PASS | Guest ended primary and fresh-console rooms; both clients transitioned to ended. |
| END-03 | PASS | Reload after end showed neutral home error, no restored history. |
| END-04 | PASS | Closed-room invite rejected with same public unavailable error. |
| END-05 | PASS | Fresh one-minute room: both saw “The conversation time is over” from real server expiration. |
| END-06 | PASS | After expiration both composers hidden and send buttons disabled. |
| END-07 | PASS | Restarted backend while 37 seconds remained; same expires_at afterward, 00:14/00:10 remaining; expired at original deadline. |

### Storage and security

| ID | Result | Direct observation |
|---|---|---|
| SEC-01 | PASS | Current session key existed; saved room.messages was []; saved values did not contain creator-first. |
| SEC-02 | PASS | Ending primary room removed its session; both storage lengths 0. |
| SEC-03 | PASS | No-session room URL denied; explicit room HTTP request returned 404/not_found; unauthenticated WS closed 1008/authentication_required, no snapshot. |
| SEC-04 | PASS | DB failure response exactly {"error":"database_unavailable"}; visible error had no SQL, DSN, credentials or stack. |
| SEC-05 | PASS | Script title rendered as literal text with zero child elements; message HTML/script likewise remained text. |

### Privacy

| ID | Result | Direct observation |
|---|---|---|
| PRIV-01 | PASS | Existing and random 64-hex room URLs in empty contexts produced identical neutral message and returned to /. |
| PRIV-02 | PASS | Used, closed and random 64-hex invites all displayed identical public unavailable message without room title/participants. |
| PRIV-03 | PASS | Malformed invite showed invitation-specific neutral error, no message-size wording or room data. |
| PRIV-04 | PASS | Existing/random no-session navigation each loaded only document, CSS, JS, favicon (200); no API/WS room lookup. |

### Interface and accessibility

| ID | Result | Direct observation |
|---|---|---|
| VIEW-01 | PASS | Desktop home and long-title chat screenshots inspected: controls intact, no overlaps/horizontal overflow; widths 1440/1440 (home), 1440/1425 (chat including scrollbar). |
| VIEW-02 | PASS | Home/chat screenshots at 390x844: scrollWidth 390, innerWidth 390; actions accessible by normal vertical scrolling. |
| VIEW-03 | PASS | Tab traversed brand/title/TTL/create; ArrowUp selected 15 minutes, Enter created; focus outline rgb(204,248,139) solid 2px; message Enter/Shift+Enter exercised. |
| VIEW-04 | PASS | Create disabled while request pending, re-enabled after 503; offline send disabled; ended composer hidden/send disabled. |
| VIEW-05 | PASS | All 60 title characters wrapped; 4000-byte message accepted without page overflow; mobile chat scrollWidth 390. |
| VIEW-06 | PASS | Primary timer fell from 30:00 to 28:45/28:39 after refresh; expiration unchanged on reconnect/restart. |
| VIEW-07 | PASS | Home/chat footer: “History is available to participants until the room closes. No E2EE.” (the footer text at the time) |
| VIEW-08 | PASS | Desktop Lighthouse snapshot: Accessibility 100, failed audits []; all 28 reported audits passed. |

## Original finding reproductions

All seven original findings were rechecked against the tested commit above:

| Finding | Verification |
|---|---|
| NC-UI-001 | VIEW-07: footer no longer claims memory-only storage or loss on restart. |
| NC-UI-002 | ROOM-01: empty title visibly “Quiet conversation”. |
| NC-UI-003 | JOIN-04/PRIV-03: malformed token gives invitation-specific wording. |
| NC-UI-004 | ROOM-05: “The server is temporarily unavailable. The room was not created, please try again.”; no nonexistent-session claim. |
| NC-UI-005 | NET-08: click “Retry” after DB recovery; chat error hidden and empty; one bubble and one persisted row. |
| NC-UI-006 | VIEW-08: Accessibility 100; failed accessibility audit list **[]**. |
| NC-UI-007 | PRIV-01/04: neutral identical text, home transition, no room lookup. |

For NC-UI-005 the primary room was
`2a5b5ab3929b72a36ee4c638febf3c348d01544e65fa588e7d7acbcc275af71e`.
Read-only SQL counted text `database-retry-once` in that room: **1**.
The successful retry displayed `4 / 500`, `failed=0`,
`errorHidden=true`, `error=""`.

Lighthouse tool output: snapshot, desktop, homepage; accessibility,
best-practices, SEO and agentic-browsing all 100; passed 28, failed 0.
Temporary browser-host reports:
`/tmp/chrome-devtools-mcp-ieGEWs/report.json` and
`/tmp/chrome-devtools-mcp-JwmG5H/report.html`.
The durable score and failed-audit list are recorded here; temporary browser
artifacts may disappear after browser restart.

## Fixtures and negative-event interpretation

- Primary room expiration remained `2026-09-26T22:39:27.05488Z` across restart.
- Fresh one-minute room expiration:
  `2026-09-27T07:06:34.707771Z`; room
  `17cab7570292da764a7f564648f137f7c53fb56f22275cabab2ccfb061a7b817`.
- Message-limit fixture room:
  `0ccf6f632ce013bfb03905758bf77743a70878273acb6b2cee05cbb18f596d7a`;
  participant:
  `b66f4dc604a6705962d1e0c68b1bf37896c32206c916254f7ad21d123dac3a64`.
  SQL inserted 499 rows with sequences 1..499, set message_count=499 and
  next_message_sequence=500, and cleared last_send_at only for this room.
  Output: BEGIN; INSERT 0 499; UPDATE 1; UPDATE 1; COMMIT.
  The room was subsequently closed through the UI.
- No session/invite tokens or encryption secret are stored in this report.
- Expected negative events: malformed/used invites, intentional DB 503,
  unauthenticated WS close, Offline ERR_INTERNET_DISCONNECTED and backend
  restart ERR_CONNECTION_REFUSED. These are not JavaScript regressions.
- Final fresh P0 Console: creator two offline failures and one connection
  refusal; guest one connection refusal; no uncaught JS exception.
  Static requests were 200, create 201, join/end 200.
- Immediately after starting PostgreSQL, readiness briefly returned 503;
  a later required readiness check returned ready before retries continued.
- Same-document hash navigation was explicitly followed by reload for fresh
  boot-path checks; stale hash-only views were not accepted as evidence.
- Screenshot evidence was viewed directly in the browser tool; the initial
  mobile chat capture also used temporary
  `/tmp/nullchat-remediation-chat-mobile.png`. Screenshots are not repository
  artifacts and may contain ephemeral invitation UI.

## Automated baseline

| Command | Result | Output |
|---|---|---|
| go test -race ./... | PASS | cmd/migrate: no tests; all eight tested packages ok (full output below). |
| go vet ./... | PASS | Exit 0, no output. |
| node --check web/app.js | PASS | Exit 0, no output. |
| docker compose run --rm test | PASS | After explicit docker compose build test from this worktree: PostgreSQL integration/race suite ok in 17.419s, exit 0. |

```text
?    nullchat/cmd/migrate [no test files]
ok   nullchat/cmd/server 1.045s
ok   nullchat/internal/chat 1.021s
ok   nullchat/internal/config 1.022s
ok   nullchat/internal/httpapi 1.155s
ok   nullchat/internal/realtime 1.021s
ok   nullchat/internal/storage/memory 1.042s
ok   nullchat/internal/storage/postgres 1.033s
ok   nullchat/web 1.047s

docker compose run --rm test
ok   nullchat/internal/storage/postgres 17.419s
```

The initial sandboxed Go commands failed before tests due to read-only
`/home/aidar/.cache/go-build`; escalated reruns above passed. The initial
combined vet/syntax command stopped before Node, so Node was run separately.
An earlier Compose run also passed in 18.013s; the final acceptance result uses
the explicitly rebuilt image. Compose's test service sets TEST_DATABASE_URL to
the separate local `nullchat_test` database and runs
`go test -tags=integration -race ./internal/storage/postgres`; this was a real
PostgreSQL run, not the unconfigured local test path.

## Final state and review

PostgreSQL and nullchat remain running for subsequent review; PostgreSQL
healthy, migrate exited 0, final /readyz returned {"status":"ready"}.
Named volume preserved throughout; no volume deletion or production access.
No application code changed during testing.

Self-review checked all 63 selected IDs, original seven reproductions,
the interruption/retest distinction, command exit codes, permanent finding
links, secret omission, and final environment state. No new defect or unresolved
finding was observed. Browser coverage is Chrome-only; transient browser
artifacts are not guaranteed durable. The report's structured observations are
the durable evidence.
