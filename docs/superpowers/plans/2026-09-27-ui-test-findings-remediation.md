# UI Test Findings Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve `NC-UI-001` through `NC-UI-007` without changing the HTTP/WebSocket protocol or weakening the existing privacy guarantees.

**Architecture:** Keep localization of the default room title in the domain validation layer, and keep transport error codes stable. Add per-request error-message overrides at the web-client boundary, make retry success clear stale transient feedback, and correct static HTML/CSS copy and accessibility defects. Verify the result against the real Compose/PostgreSQL/browser stack before moving findings to `Resolved`.

**Tech Stack:** Go 1.24, embedded HTML/CSS/vanilla JavaScript, PostgreSQL 17, Docker Compose, Chrome/Lighthouse

**Spec:** `docs/UI_TEST_FINDINGS.md`

> **Historical note:** when this plan was written the UI was in Russian. Quoted UI strings below are English translations of the Russian originals; the UI has since been translated to English and its current strings differ in places.

## Global Constraints

- Preserve the stable backend error codes and API shapes documented in `SPEC.md`; this remediation changes presentation, not the wire protocol.
- A room URL without `sessionStorage` must not trigger HTTP or WebSocket access and must not reveal whether the room exists.
- A malformed, used, closed, or random invitation must reveal no room title, participant, or existence details.
- A retry of a message must retain the existing `client_message_id`, so PostgreSQL idempotency continues to prevent duplicates.
- Confirmed history must survive backend restart and remain available to authorized participants until the room closes or expires.
- Do not delete the Compose PostgreSQL named volume during verification.
- Follow `docs/UI_TESTING_RUNBOOK.md`; browser results count as `PASS` only when directly observed.

## Review Focus

- A valid-looking but unavailable invite still displays `room_unavailable`; only malformed input receives the neutral invalid-invite copy (covered by Task 2 tests and Task 5 cases `JOIN-02`, `JOIN-04`, `PRIV-02`, `PRIV-03`).
- A PostgreSQL outage during create must not claim that a session was saved, while an outage after chat entry may still say that the existing session is retained (covered by Task 2 tests and Task 5 cases `ROOM-05`, `NET-07`).
- A failed message retried after database recovery is acknowledged exactly once and clears the old banner without hiding a new server rejection (covered by Task 3 tests and Task 5 case `NET-08`).
- Real and random `#room=<64 hex>` URLs without local credentials remain network-indistinguishable and receive identical neutral text (covered by Task 2 tests and Task 5 cases `PRIV-01`, `PRIV-04`).
- Small text on every actual background, including placeholders and decorative arrow text, remains above the Lighthouse contrast threshold in desktop and `390x844` layouts (covered by Task 4 assertions and Task 5 cases `VIEW-02`, `VIEW-08`).

---

### Task 1: Persistence Copy and Localized Default Room Title

**Files:**
- Modify: `internal/chat/validation_test.go:8-27`
- Modify: `internal/chat/validation.go:19-28`
- Modify: `web/app_test.go`
- Modify: `web/index.html:74`

**Interfaces:**
- Consumes: `chat.NormalizeTitle(title string) (string, error)` and the embedded `web` assets.
- Produces: `NormalizeTitle("  ")` returning the Russian default title (meaning "Quiet conversation") and footer copy consistent with PostgreSQL persistence.

- [ ] **Step 1: Write failing tests for the localized default and persistence copy**

Change the empty-title assertion in `TestNormalizeTitleAndMessage` to require the Russian default title (meaning "Quiet conversation"). Add `TestFrontendCopyMatchesPersistenceContract` in `web/app_test.go`; read `index.html`, require the exact sentence "History is available to participants until the room closes. No E2EE.", and reject the old phrase "history is kept in memory".

- [ ] **Step 2: Run the focused tests and verify both failures**

Run: `go test ./internal/chat ./web`

Expected: FAIL because `NormalizeTitle` returns the English `Quiet conversation` and the embedded footer still describes in-memory history.

- [ ] **Step 3: Implement the localized default title**

In `NormalizeTitle(title string) (string, error)`, preserve trimming and validation but return the Russian default title (meaning "Quiet conversation") for the empty normalized title. Do not move the fallback into JavaScript: both storage implementations must persist the same server-authoritative title.

- [ ] **Step 4: Replace the stale footer statement**

In `web/index.html`, replace only the storage sentence with "History is available to participants until the room closes. No E2EE." Keep the copyright and health link unchanged.

- [ ] **Step 5: Run the focused tests**

Run: `go test ./internal/chat ./web`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chat/validation.go internal/chat/validation_test.go web/index.html web/app_test.go
git commit -m "fix: align room defaults with persisted UI"
```

### Task 2: Operation-Specific Errors and Neutral Room Recovery Copy

**Files:**
- Modify: `web/app_test.go`
- Modify: `web/app.js:7-34,234-255,301-310`

**Interfaces:**
- Consumes: unchanged backend error codes such as `invalid_input`, `database_unavailable`, and `room_unavailable`.
- Produces: `api(path, data, token, key = '', errorOverrides = {})`; an override object maps an error code to copy for one operation only.

- [ ] **Step 1: Write failing frontend contract tests**

Add `TestFrontendUsesOperationSpecificErrors` to assert that `app.js` contains the five-argument `api` signature, resolves failures in the order `errorOverrides[result.error] || errors[result.error] || fallback`, and supplies these exact call-site overrides:

```text
create database_unavailable -> The server is temporarily unavailable. The room was not created, please try again.
join invalid_input -> This invite is not valid. Check the link or ask for a new one.
```

Add `TestFrontendRoomURLWithoutSessionUsesNeutralCopy` to require "The room could not be opened. Use a valid invite or create a new room." and reject "This tab has no session for the room".

- [ ] **Step 2: Run the web tests and verify failure**

Run: `go test ./web`

Expected: FAIL because `api` has no override parameter and the old room/session copy is present.

- [ ] **Step 3: Add request-local error overrides**

Change the function signature to `async function api(path, data, token, key = '', errorOverrides = {})`. On a non-OK response, prefer `errorOverrides[result.error]`, then the shared `errors` entry, then the existing generic fallback. Pass only the create override at `/api/v1/rooms` and only the join override at `/api/v1/join`; leave end-chat and WebSocket error handling on the shared map.

- [ ] **Step 4: Make missing local room credentials neutral**

In the `params.has('room')` branch, keep the redirect to `/` and the no-network behavior, but replace the error with "The room could not be opened. Use a valid invite or create a new room."

- [ ] **Step 5: Run syntax and focused tests**

Run: `node --check web/app.js && go test ./web`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/app.js web/app_test.go
git commit -m "fix: contextualize room request errors"
```

### Task 3: Clear the Stale Database Banner on Successful Message Retry

**Files:**
- Modify: `web/app_test.go`
- Modify: `web/app.js:275-282`

**Interfaces:**
- Consumes: `transmit(entry, socket = state.socket) -> boolean`, where `true` means the message was placed on the active WebSocket.
- Produces: the manual retry handler clears `chat-error` only when `transmit(entry)` returns `true`; a synchronous retry failure keeps or replaces visible error feedback.

- [ ] **Step 1: Write the failing retry-handler test**

Add `TestFrontendSuccessfulManualRetryClearsStaleError`. Slice the source between the `messages` click handler and the `message-form` submit handler, then assert that it captures `const sent = transmit(entry);` and contains `if (sent) showError('chat-error');`. This pins the success-only behavior without changing message identity or acknowledgement handling.

- [ ] **Step 2: Run the focused test and verify failure**

Run: `go test ./web -run TestFrontendSuccessfulManualRetryClearsStaleError -v`

Expected: FAIL because the existing click handler ignores `transmit`'s return value.

- [ ] **Step 3: Implement success-only banner clearing**

Store the result of `transmit(entry)` in `sent`. Clear `chat-error` when it is true, then render messages as before. Do not clear on `false`, do not create a new client message ID, and let a later WebSocket `error` event display any new rejection.

- [ ] **Step 4: Run syntax and web tests**

Run: `node --check web/app.js && go test ./web`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/app.js web/app_test.go
git commit -m "fix: clear stale error after message retry"
```

### Task 4: Accessibility Name and Contrast Corrections

**Files:**
- Modify: `web/app_test.go`
- Modify: `web/index.html:16`
- Modify: `web/style.css:1-10,185-187,248-266,316-340`

**Interfaces:**
- Consumes: the visible brand text `n_nullchat.` and existing dark backgrounds `--bg`, `--panel`, and input background `#141915`.
- Produces: accessible brand name `n_nullchat. — home` (Russian original: "to the home page") and `--muted-subtle: #84907f`, whose contrast is at least 5.05:1 on the darkest affected panel backgrounds.

- [ ] **Step 1: Write failing static accessibility tests**

Add `TestFrontendStaticAccessibilityContract` to read `index.html` and `style.css`, require `aria-label="n_nullchat. — home"` (in its Russian form at the time), require `--muted-subtle:#84907f`, and require `var(--muted-subtle)` on `.flow .flow-arrow`, `label span`, `input::placeholder,textarea::placeholder`, `.card-footnote`, and `.principle-num`.

- [ ] **Step 2: Run the focused test and verify failure**

Run: `go test ./web -run TestFrontendStaticAccessibilityContract -v`

Expected: FAIL because the logo label omits visible label content and the subtle color token does not exist.

- [ ] **Step 3: Align the logo's accessible and visible names**

Change only the brand link label to `aria-label="n_nullchat. — home"` (in its Russian form at the time), retaining the visible markup and link destination.

- [ ] **Step 4: Consolidate low-contrast small text**

Add `--muted-subtle:#84907f` under `:root`. Replace the five low-contrast text colors named in Step 1 with `var(--muted-subtle)`; do not globally brighten `--muted` or alter component backgrounds.

- [ ] **Step 5: Run the focused automated checks**

Run: `go test ./web && node --check web/app.js`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/index.html web/style.css web/app_test.go
git commit -m "fix: pass homepage accessibility audits"
```

### Task 5: Real-Stack Regression and Findings Closure

**Files:**
- Create: `docs/test-reports/2026-09-27-ui-findings-remediation.md`
- Modify: `docs/UI_TEST_FINDINGS.md`

**Interfaces:**
- Consumes: Tasks 1-4, `docs/UI_TEST_CASES.md`, `docs/UI_TESTING_RUNBOOK.md`, and the `testing-nullchat-ui` skill.
- Produces: direct browser evidence for every P0/P1 case and synchronized `Resolved` entries for `NC-UI-001` through `NC-UI-007` only when their original reproductions pass.

- [ ] **Step 1: Capture the verification baseline**

Record `git status --short`, `git rev-parse --short HEAD`, date, browser version, existing dirty files, and initial container state in the new report. Apply migrations, start `postgres` and `nullchat`, and require `curl --fail http://127.0.0.1:8080/readyz` to succeed. Never run `docker compose down -v`.

- [ ] **Step 2: Run the full P0 and P1 browser matrix**

Use separate isolated browser contexts for creator, guest, and outsider. Execute all cases in `docs/UI_TEST_CASES.md`, following the runbook's ordering and outage/restart instructions. Record every case as `PASS`, `FAIL`, or `BLOCKED`; include Console and Network observations.

- [ ] **Step 3: Explicitly recheck all seven original reproductions**

Confirm `ROOM-01`, `ROOM-05`, `JOIN-04`, `NET-08`, `PRIV-01`, `PRIV-03`, `PRIV-04`, `VIEW-07`, and `VIEW-08`. For `NET-08`, verify one persisted message and no stale `database_unavailable` banner. For `VIEW-08`, save the Lighthouse score and failed-audit list; expected result is no failed accessibility audit.

- [ ] **Step 4: Run the complete automated baseline**

Run:

```bash
go test -race ./...
go vet ./...
node --check web/app.js
docker compose run --rm test
```

Expected: all commands PASS; Compose PostgreSQL tests must run rather than skip.

- [ ] **Step 5: Synchronize the permanent findings registry**

If and only if each original reproduction passes, move `NC-UI-001` through `NC-UI-007` from `Open findings` to `Resolved findings`, set `Status: Resolved`, add the verification date and the short commit captured in Step 1, and link `docs/test-reports/2026-09-27-ui-findings-remediation.md`. Any reproduction that still fails remains `Open`, retains its stable ID, and is reported as a failure rather than being silently closed.

- [ ] **Step 6: Record final environment state and commit evidence**

Document whether `postgres` and `nullchat` remain running, then run `git diff --check`.

```bash
git add docs/UI_TEST_FINDINGS.md docs/test-reports/2026-09-27-ui-findings-remediation.md
git commit -m "test: verify UI findings remediation"
```
