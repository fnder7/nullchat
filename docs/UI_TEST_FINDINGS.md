# UI test findings

The standing register of issues found by UI/E2E runs. One-off results and
environments live in `docs/test-reports/`; this file holds stable finding IDs,
status and reproduction steps.

Statuses: `Open`, `Resolved`, `Accepted`. Severity: `Critical`, `High`, `Medium`,
`Low`.

## Open findings

None. All seven original findings were re-verified on 2026-09-27.

## Resolved findings

### NC-UI-001 — the footer describes an outdated history storage model

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Medium`
- Cases: `VIEW-07`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: open the home page or the chat and read the footer text.
- Expected: the text matches PostgreSQL persistence and does not promise that
  confirmed history is lost on a backend restart.
- Actual: the footer says history is kept in memory until the room closes or the
  server restarts.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-002 — the default room title does not match the locale

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Low`
- Cases: `ROOM-01`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: leave the title empty and create a room.
- Expected: a neutral Russian title consistent with the placeholder and the UI
  (the UI was in Russian at the time).
- Actual: `Quiet conversation` is shown.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-003 — a malformed invite gets an error about the message

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Medium`
- Cases: `JOIN-04`, `PRIV-03`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: open `/#invite=invalid-token` and press "Join the room".
- Expected: a neutral invalid-invite error without room details.
- Actual: "Check your input. A message must contain 1 to 4000 bytes of text."
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-004 — create during a DB outage talks about a session that does not exist

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Low`
- Cases: `ROOM-05`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: stop PostgreSQL and try to create a room.
- Expected: a message about temporary unavailability and that retrying the create
  is safe.
- Actual: "The server is temporarily unavailable. Your session is saved, try
  again later", although no session has been created yet.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-005 — a successful retry does not hide the old DB error

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Medium`
- Cases: `NET-08`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: send a message while the DB is stopped, bring PostgreSQL back and press
  "Retry" on the message.
- Expected: the message is confirmed exactly once and the temporary error is
  hidden.
- Actual: the message is saved once, but the `database_unavailable` banner stays
  until the next regular send.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-006 — the home page does not pass all accessibility audits

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Medium`
- Cases: `VIEW-08`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: run a Lighthouse accessibility snapshot of the home page.
- Expected: no failed accessibility audits.
- Actual: score `96`; failed `color-contrast` for small text and
  `label-content-name-mismatch` for the logo.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.

### NC-UI-007 — the no-session text can be read as confirming the room exists

- Status: `Resolved`
- Verified: `2026-09-27`, commit `4f58e64`.
- Verification: [UI findings remediation acceptance](test-reports/2026-09-27-ui-findings-remediation.md).
- Severity: `Low`
- Cases: `PRIV-01`, `PRIV-04`
- Found: `2026-09-27`, commit `15d3b78`
- Steps: in a clean browser context, open an existing or random
  `/#room=<64 hex>` without `sessionStorage`.
- Expected: a neutral text that does not imply a specific room exists, for
  example "The room could not be opened. Use a valid invite or create a new
  room."
- Actual: "This tab has no session for the room. Ask for a new invite or create
  your own room."
- Security evidence: a real and a random room ID give the same result and do not
  trigger an HTTP/WebSocket request; actual enumeration was not confirmed.
- Evidence: `docs/test-reports/2026-09-27-ui-regression.md`.
