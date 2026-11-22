# Guest Screen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give an invite link its own guest screen with a real join form, a field-level password error and calm terminal states. The server stays unchanged.

**Architecture:** A new pure module `web/invite.js` holds two pieces of logic, both unit-tested with `node --test` like `linkify.js` and `trust.js`: the check that runs when the page loads, and the mapping from a join response to a state. `index.html` gets a sibling `<main id="invite">`, and the old `#invite-panel` leaves the create card. `app.js` renders that screen from the derived state and drives the join form.

**Tech Stack:** Vanilla ES modules, HTML, CSS; Go `embed` + string-contract tests in `web/app_test.go`; `node --test`.

**Spec:** `docs/superpowers/specs/2026-11-22-guest-screen-design.md`

## Global Constraints

- No server, protocol or error-code change. `room_unavailable` stays one response for used, revoked, closed, expired and unknown invites.
- No `innerHTML`/`insertAdjacentHTML` (CSP with Trusted Types; `TestFrontendBuildsMessageLinksSafely` enforces this). Existing tokens in `web/style.css` only. Text is at least 12px, contrast at least 4.5:1.
- Invite format: `#invite=<64-char token>&k=<base64url, 32 bytes>[&pw=1]`.
- The `room_unavailable` text in the `errors` map stays unchanged; other screens use it.
- Copy, verbatim from the spec:
  - **ready.**
    - Heading: "You're invited to a private conversation".
    - Text: "This link holds one seat and works once. When you join, the room opens for the two of you."
    - Facts: "Messages are encrypted in your browser. The server only sees ciphertext." · "The room closes by itself when its time runs out." · "No sign-up, no profile."
    - Label: "Room password". Hint: "The person who invited you should send it separately from the link."
    - Button: "Join the conversation". Link: "Or create your own room".
  - **joining.** Button "Joining…"; status "Joining the conversation…".
  - **Field errors.** "Wrong password, or the link is damaged. After 5 wrong attempts the link stops working." · "Enter the room password."
  - **closed.**
    - Heading: "This link can't be opened".
    - Text: "It may have been used already, turned off after wrong passwords, or the conversation is over. For privacy, nullchat doesn't say which."
    - Second line: "If you haven't opened this link before, tell the person who sent it, using a different app, and ask for a new one."
    - Button: "Create your own room".
  - **damaged.**
    - Heading: "This link is incomplete".
    - Text: "Part of the link is missing. Ask for the full link again, and copy it in one piece."
    - Button: "Create your own room".
  - **insecure.**
    - Heading: "Open this link over HTTPS".
    - Text: "Encryption only works on a secure connection. Open the link at its https:// address."
    - No button.

## Review Focus

1. **Enter in the password field** submits once, and a double Enter or a click during `joining` does not send a second request. Pinned in Task 3.
2. **A retry after a transient error** (for example `rate_limited`) reuses the same prepared request and `Idempotency-Key`, so a join that succeeded server-side is replayed rather than reported as `room_unavailable`. Pinned in Task 3.
3. **Editing the password after a wrong-password error** clears `aria-invalid` and the field message, and resets the prepared request. Pinned in Task 3.
4. **The link without `pw=1` gets `join_proof_invalid`** (a damaged secret). The message appears under the button, since there is no field. Pinned in Task 3.
5. **The home page with no fragment** is unchanged for the creator. `#invite` stays hidden, and the create form works. Pinned in Task 2.

---

### Task 1: Pure invite-state module

**Files:**
- Create: `web/invite.js`, `web/invite.test.mjs`
- Modify: `web/embed.go` (add `invite.js` to `//go:embed`); `web/manifest_test.go` (add `"invite.js"`); `.gitlab-ci.yml` and `.github/workflows/ci.yml` (add `web/invite.test.mjs` to `node --test`)

**Interfaces:**
- Produces:
  - `linkState({ invite, k, secure }) -> 'ready' | 'damaged' | 'insecure'`. Precedence: `!secure` → `'insecure'`; then `invite` not exactly 64 characters, or `k` not decoding to exactly 32 bytes of base64url → `'damaged'`; otherwise `'ready'`. Decode base64url locally: `-`/`_`, no padding, and an invalid character means damaged. Do not import `crypto.js`.
  - `joinOutcome(code) -> 'closed' | 'damaged' | 'field' | 'transient'`: `'room_unavailable'` → `'closed'`; `'invalid_input'` → `'damaged'`; `'join_proof_invalid'` → `'field'`; anything else, including `undefined`, → `'transient'`.

- [ ] **Step 1: Write the failing tests** in `web/invite.test.mjs`:
  - `insecure wins`: `linkState({ invite: 'a'.repeat(64), k: <valid>, secure: false })` is `'insecure'`.
  - `well-formed link is ready`: a 64-char invite plus a 43-char base64url key that decodes to 32 bytes → `'ready'`.
  - `short token is damaged`: 63 characters → `'damaged'`.
  - `missing, short or invalid key is damaged`: `k` as `''`, as a 16-byte value, and containing `'+'` → `'damaged'` each time.
  - `join codes map to states`: the four mappings above, plus `joinOutcome('room_full')` and `joinOutcome(undefined)` → `'transient'`.
- [ ] **Step 2: Run to verify it fails.** Run `node --test web/invite.test.mjs`. Expected: FAIL, `Cannot find module './invite.js'`.
- [ ] **Step 3: Implement `web/invite.js`** with the two exports. Wire it into embed, manifest and both CI files.
- [ ] **Step 4: Run to verify it passes.** Run `node --test web/*.test.mjs && go test ./web/`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): pure link check and join outcome for the guest screen`

---

### Task 2: Guest screen markup and boot routing

**Files:**
- Modify: `web/index.html`:
  - Add `<main id="invite" class="invite-screen" hidden>` between `#home` and `#chat`. It contains `<section id="invite-card" class="invite-card" aria-labelledby="invite-title">` with `<h1 id="invite-title" tabindex="-1">` and `<div id="invite-body">`.
  - Remove `#invite-panel` from the create card.
- Modify: `web/app.js`:
  - Boot code: when the fragment has `invite`, hide `#home`, show `#invite`, and call `renderInvite(linkState(...))`. This also covers the no-`crypto.subtle` case: an invite URL goes to `insecure` instead of the home error.
  - Remove the `params.has('invite')` branch that edits the create card.
  - `enter()` also hides `#invite`.
  - Remove the old `#join-password` / `#join-button` listeners. Task 3 rebuilds the join flow.
- Modify: `web/style.css` (`.invite-screen`, `.invite-card`, `.invite-facts`, and the neutral terminal tone; remove `.invite-copy` if it is no longer used)
- Test: `web/app_test.go`

**Interfaces:**
- Consumes: `linkState` from Task 1.
- Produces:
  - `renderInvite(state: 'ready' | 'closed' | 'damaged' | 'insecure') -> void`. It builds `#invite-body` with createElement and textContent, using the spec copy, and sets `#invite-title`.
    - **ready:** contains `<form id="join-form" novalidate>`. When `pw=1`, the form has `<input id="join-password" type="password" autocomplete="off" maxlength="200" aria-describedby="join-password-hint">` plus a hint and a `<p id="join-field-error" class="error" hidden>`. The form also has `<button id="join-button" class="primary" type="submit">`, `<p id="join-error" class="error" role="alert" hidden>` and `<p id="join-status" class="sr-only" role="status">`.
    - **Terminal states** move focus to `#invite-title`; `ready` does not.
  - `state.inviteShown: string` — the last rendered state.

- [ ] **Step 1: Write the failing test** `TestFrontendInviteHasItsOwnScreen`:
  - `index.html` contains `<main id="invite"` and no `id="invite-panel"`;
  - `#invite` sits outside `<main id="home"`… its `</main>`;
  - `#invite-title` has `tabindex="-1"`;
  - `app.js` contains `from './invite.js'` and `function renderInvite(`;
  - every spec copy string for `ready`, `closed`, `damaged` and `insecure` appears verbatim in `app.js`;
  - `renderInvite` contains `type = 'submit'` (or `type="submit"`) and `join-form`;
  - the body of `function enter(` hides `invite` (Review Focus 5 is pinned by the next item);
  - the create form markup (`<form id="create-form">`) is still in `#home`, unchanged.
- [ ] **Step 2: Run to verify it fails.** Run `go test ./web/ -run TestFrontendInviteHasItsOwnScreen`. Expected: FAIL on `<main id="invite"`.
- [ ] **Step 3: Implement** the markup, the boot routing and `renderInvite` for all four states. Leave join submission to Task 3: the form exists, but the submit handler is added there.
- [ ] **Step 4: Run to verify it passes.** Run `go test ./web/ && node --check web/app.js && node --test web/*.test.mjs`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): dedicated guest screen for invite links`

---

### Task 3: Join flow on the guest screen

**Files:**
- Modify: `web/app.js`:
  - `api()`: attach the server code to the thrown error, `err.code = result.error`, keeping the message behaviour.
  - `prepareJoin()`: read the password from `#join-password` if present. Throw a plain `Error` with `code = 'password_empty'` for an empty required password.
  - Add the submit handler for `#join-form`, delegated on `#invite-body` or bound in `renderInvite('ready')`.
- Test: `web/app_test.go`

**Interfaces:**
- Consumes: `joinOutcome` from Task 1; `renderInvite` and the ids from Task 2; the existing `prepareJoin()`, `prepared.join`, `enter()`, `checkStorage()` and `errors`.
- Produces: `showJoinFieldError(text: string) -> void`.
  - **When the password field exists:** it writes the text to `#join-field-error`, sets `aria-invalid="true"` on `#join-password` and adds `join-field-error` to its `aria-describedby`, then focuses the field.
  - **When there is no field** (no `pw=1`): it writes the text to `#join-error` instead (Review Focus 4).

- [ ] **Step 1: Write the failing test** `TestFrontendJoinFlowOnGuestScreen`. The join submit handler body must:
  - call `event.preventDefault()`;
  - return early when `#join-button` is disabled (Review Focus 1);
  - set the button text to `'Joining…'`;
  - write `'Joining the conversation…'` to `#join-status`;
  - call `joinOutcome(`;
  - call `renderInvite('closed')` and `renderInvite('damaged')`;
  - call `showJoinFieldError(`.

  It must not set `prepared.join = null` in the `transient` path; it does so only after success (Review Focus 2).

  Also:
  - `api(` sets `.code =`;
  - the `#join-password` input listener resets `prepared.join = null`, removes `aria-invalid` and hides `#join-field-error` (Review Focus 3);
  - the spec strings "Wrong password, or the link is damaged. After 5 wrong attempts the link stops working." and "Enter the room password." appear verbatim.
- [ ] **Step 2: Run to verify it fails.** Run `go test ./web/ -run TestFrontendJoinFlowOnGuestScreen`. Expected: FAIL.
- [ ] **Step 3: Implement the handler.**
  - Run `checkStorage()`, then `prepareJoin()`, then `api('/api/v1/join', …, request.key)`.
  - **On success:** clear `prepared.join`, then `enter(...)` as today.
  - **On error:** map `e.code` with `joinOutcome`:
    - `closed` / `damaged` → `renderInvite(...)`;
    - `field`, or `code === 'password_empty'` → `showJoinFieldError(...)` with the spec copy;
    - `transient` → the message under the button (`e.message`), then re-enable the button and restore its label "Join the conversation".
- [ ] **Step 4: Run to verify it passes.** Run `go test ./web/ && node --check web/app.js && node --test web/*.test.mjs`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): join from the guest screen with field-level password errors`

---

### Task 4: Browser verification and docs

**Files:**
- Modify: `docs/UI_TEST_CASES.md` (cases GUEST-01… for each state and the Review Focus items)

- [ ] **Step 1: Rebuild and check what is served.** Run `docker compose up --build -d`, then confirm the served `/app.js` matches `web/app.js` by shasum.
- [ ] **Step 2: Walk the screen in new tabs, at desktop width and at 375 px.**
  - **Arrival:** a valid invite with and without a password. Check that Join is in the first mobile viewport.
  - **Password field:**
    - Enter submits.
    - An empty password gives the field error.
    - A wrong password gives the field error with `aria-invalid`.
    - Editing the field clears the error.
    - The correct password opens the chat.
  - **Reopening the used link:** the `closed` state, with focus on the heading.
  - **Damaged links:** a truncated `k`, and a 63-char token. Both give `damaged` without any network request (check with `read_network_requests`).
  - **Home without a fragment:** unchanged.

  Expected: everything matches the spec, with no console errors. Record any case you could not trigger, such as `insecure`.
- [ ] **Step 3: Run the detector** with `"/Users/fnder77/.claude/plugins/cache/impeccable/impeccable/4.10.0/skills/impeccable/scripts/impeccable" detect --json web/index.html`. Expected: no new findings inside `#invite`.
- [ ] **Step 4: Run the full test suite.** Run `go test -race ./... && node --check web/app.js && node --test web/*.test.mjs`. Expected: PASS.
- [ ] **Step 5: Commit** `docs: UI test cases for the guest screen`. Do not push; the controller asks the user.
