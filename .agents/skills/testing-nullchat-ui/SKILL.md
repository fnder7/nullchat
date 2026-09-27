---
name: testing-nullchat-ui
description: Use when validating nullchat through its browser UI after any repository change, including smoke/regression runs and PostgreSQL, WebSocket, reconnect, persistence, invitation, privacy, or release checks.
---

# Testing nullchat UI

## Overview

Test the real local stack in a browser and leave auditable artifacts. A Go test
never turns an unobserved UI case into `PASS`.

## Required sources

Before testing, find the repository root and read:

- `docs/UI_TEST_CASES.md` — case IDs and expectations.
- `docs/UI_TESTING_RUNBOOK.md` — environment, fixtures, and commands.
- `docs/UI_TEST_FINDINGS.md` — known findings to recheck.

Do not copy the matrix into a new ad-hoc checklist.

## Select scope

| Trigger | Required scope |
|---|---|
| Any change | P0 `UI-01`—`UI-09`, targeted P1 for the affected behavior, and the automated baseline |
| PostgreSQL, WebSocket, session, invite, storage, security, or release change | Full P0 and P1 |
| User explicitly requests full regression | Full P0 and P1 |

The user may explicitly narrow scope. Record omitted cases as skipped, never
successful.

## Execution contract

1. Record commit, dirty worktree, browser, and environment before the run.
2. Use the Compose application with migrations applied and `/readyz` healthy.
   Never delete the named volume unless the user explicitly requests it.
3. Use separate isolated browser contexts for creator, guest, and outsider.
4. Observe UI, Console, and Network. Exercise restart, database outage,
   reconnect, and anti-enumeration when selected by the matrix.
5. Use `PASS` only for directly observed expected behavior, `FAIL` for a
   mismatch, and `BLOCKED` for an unavailable prerequisite. If Chrome is
   unavailable, UI cases are `BLOCKED`; backend tests are separate evidence.
6. Run the automated baseline from the runbook even for a small UI change.
7. Do not fix discovered defects during a testing-only request.

## Required artifacts

Create a new `docs/test-reports/YYYY-MM-DD-<scope>.md`; do not overwrite an
earlier run. Include scope, each selected ID, evidence, skipped cases, command
results, and final container state.

Maintain findings separately in `docs/UI_TEST_FINDINGS.md`:

- Reuse the stable finding ID when the same issue reproduces.
- A reproduced known issue remains `Open` and its case remains `FAIL`.
- Add a new finding with severity, affected cases, steps, expected/actual, and
  evidence.
- Mark a finding `Resolved` only after its original reproduction no longer
  fails; link the verifying report.
- Do not bury findings only inside the run report.

## Stop conditions

Finish only when each selected case and automated command has a result, findings
are synchronized, and environment state is recorded. Treat unauthorized room
access, room enumeration, or loss of a confirmed message as critical: report it,
continue only safe read-only checks, and do not improvise a fix.

## Common mistakes

- Same browser context for both participants: invalid session-isolation test.
- Network Offline without closing the existing WebSocket: reconnect was not
  actually tested.
- Unit/integration green, UI unobserved: UI stays `BLOCKED`.
- Expected negative HTTP/WS errors counted as console regressions: distinguish
  deliberate failure evidence from unexpected errors.
- Findings written only in a dated report: update the permanent registry too.
