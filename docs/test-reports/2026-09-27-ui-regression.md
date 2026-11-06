# UI regression report — 2026-09-27

- Commit: `15d3b78`
- Environment: Docker Compose, PostgreSQL 17, local backend
- Browser: controlled Chrome, independent isolated contexts
- Scope: full P0 and P1 from `docs/UI_TEST_CASES.md`

## Summary

- `PASS`: 56
- `FAIL`: 7
- `BLOCKED`: 0
- No defects causing data loss or giving an outsider access to a room were found.
- PostgreSQL persistence works after a backend restart and a temporary DB outage.
- Full findings: `docs/UI_TEST_FINDINGS.md` (`NC-UI-001`–`NC-UI-007`).

## P0

| ID | Result | Comment |
|---|---|---|
| UI-01—UI-09 | PASS | Creation, two independent participants, message exchange, refresh, reconnect, backend restart, ending and the console check were done. Console errors during the deliberate offline/restart were expected. |

## P1

| Group / ID | Result | Comment |
|---|---|---|
| ROOM-01 | FAIL | An empty title becomes the English `Quiet conversation`, although the whole UI and the placeholder are in Russian. |
| ROOM-02—ROOM-04 | PASS | 60 characters are accepted; TTL 1/15/30/60 matches the server's `expires_at`; a retry with the same idempotency key returns the same room and session. |
| ROOM-05 | FAIL | The 503 is handled functionally, but create shows "Your session is saved" although no session has been created yet. |
| JOIN-01—JOIN-03, JOIN-05—JOIN-06 | PASS | Joining, a join retry, refusing a third participant, no access via a room URL alone, and refresh work. |
| JOIN-04 | FAIL | A malformed invite shows the text "A message must contain 1 to 4000 bytes", which has nothing to do with the invite. |
| MSG-01—MSG-11 | PASS | Enter/Shift+Enter, empty input, the 4000-byte boundary, oversize, XSS, retry/dedup, ordering, persistence and the 500 limit were checked. 499 messages were prepared as a fixture in the local DB; the 500th was accepted, the 501st got `message_limit`. |
| NET-01—NET-07 | PASS | Offline, auto/manual reconnect, a pending message, backend restart and a DB outage were checked. |
| NET-08 | FAIL | After the DB recovers, retry saves the message exactly once, but the old `database_unavailable` banner stays until the next regular send. |
| END-01—END-07 | PASS | Ending by both roles, refresh, the invite after closing, a real 1-minute TTL and keeping the original `expires_at` work. |
| SEC-01—SEC-05 | PASS | No history in `sessionStorage`; the session is removed; no access without a token; internal DB details are not shown; title/message XSS does not execute. |
| PRIV-01—PRIV-02, PRIV-04 | PASS | Real and random room URLs are indistinguishable; the backend is not queried. Used/closed/random well-formed invites return the same public state. |
| PRIV-03 | FAIL | No leak, but a malformed invite gets an irrelevant error about the message. |
| VIEW-01—VIEW-06 | PASS | Desktop/mobile `390x844`, keyboard focus/Enter, disabled states, a long title and the timer were checked; no horizontal overflow. |
| VIEW-07 | FAIL | The footer claims history is kept in memory and lost on a backend restart; this contradicts the current PostgreSQL persistence. |
| VIEW-08 | FAIL | Lighthouse accessibility: 96/100, `color-contrast` and `label-content-name-mismatch` failed. |

## Findings

Detailed findings, reproduction steps and privacy conclusions are kept in the
standing register `docs/UI_TEST_FINDINGS.md`. This file holds only the results of
this particular run.

## Automated checks

| Check | Result |
|---|---|
| `go test -race ./...` | PASS |
| `go vet ./...` | PASS |
| `node --check web/app.js` | PASS |
| `docker compose run --rm test` | PASS (`internal/storage/postgres`, 17.645s) |

## Environment state

The test `postgres` and `nullchat` were left running; the named volume was not
deleted.
