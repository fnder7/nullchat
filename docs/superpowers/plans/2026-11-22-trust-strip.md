# Trust Strip Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the invite link and the safety-code check out of the sidebar into one pinned strip above the message list, with a local "verified" status shown as a badge in the conversation header.

**Architecture:** A new pure module `web/trust.js` decides which strip state applies and what the badge says (unit-tested with `node --test`, like `linkify.js`). `app.js` gets one `renderTrust()` that builds the strip DOM from that state, called wherever room, key or end state changes. A native `<dialog>` replaces `confirm()` for every "end for both" path.

**Tech Stack:** Vanilla ES modules, HTML, CSS; Go `embed` + string-contract tests in `web/app_test.go`; `node --test` for pure JS.

**Spec:** `docs/superpowers/specs/2026-11-22-trust-strip-design.md`

## Global Constraints

- No dependencies. DOM is built with `createElement`/`textContent`; never `innerHTML` or `insertAdjacentHTML` (`TestFrontendBuildsMessageLinksSafely` enforces this). CSP: `trusted-types 'none'`.
- The strip and the badge live outside `#messages` (`role="log"`).
- Verification status is local: stored in `state.e2ee.verified` (`'match' | 'later' | undefined`), written by `save()`. No backend, protocol or WebSocket change.
- No automatic focus moves, except: when a state change removes the focused control *inside the strip*, focus goes to `#trust-title` (`tabindex="-1"`).
- Every "end for both" path asks for confirmation through `#end-dialog`.
- The safety code is at least 24px, monospace; any text in the strip is at least 12px; text contrast at least 4.5:1, using the existing tokens (`--accent`, `--error`, `--muted`, …) in `style.css:2-12`.
- Copy in the strip, verbatim from the spec:
  - invite heading "Send this link to your contact"; warning "Anyone who opens this link takes the seat. Send it somewhere private."; with password "Send the password separately."
  - waiting "The invite was revoked after 5 wrong password attempts. End the chat and create a new room."
  - verify heading "Is this really them?"; text "Compare this code with your contact by voice or in another app. If it matches, you are talking to the right person."; buttons "It matches", "It doesn't match", "Later"
  - verified "You confirmed this code matches."; links "Compare again", "Hide"
  - broken heading "Key verification failed" plus today's `keyFailure()` note; button "End the chat"
  - badges "✓ Verified", "Not verified", "Key error"
  - dialog: end "End the conversation? The room and its history will be gone for both of you."; mismatch "The codes don't match. The person on the other end may not be your contact. End the conversation for both?"; broken "Key verification failed, so this conversation is not safe. End it for both?"; buttons "End for both", "Cancel"
  - status announcements "Your contact joined. Compare the safety code." and "Marked as verified."

## Review Focus

1. **Reload in each state.** After a reload in `verified` or `later`, the badge and collapsed strip come back the same, because `verified` sits in `state.e2ee` and the code is recomputed by `establishKey()`. Pinned by the trust.js tests plus a Go assertion in Task 4.
2. **The guest joins while the creator's focus is on Copy link.** Focus lands on `#trust-title`, not `<body>`. Pinned in Task 4.
3. **Mismatch dialog cancelled.** The strip stays in `verify`: the mismatch path never writes `verified`. Pinned in Task 4.
4. **The room ends while the dialog is open** (peer ended it or TTL ran out). `finish()` closes `#end-dialog`, so nobody confirms ending a room that is already gone. Pinned in Task 2.
5. **Invite revoked while the creator watches the invite strip.** The strip switches to `waiting`, and the old `chat-error` copy is not shown a second time. Pinned in Task 3.

---

### Task 1: Pure trust-state module

**Files:**
- Create: `web/trust.js`
- Create: `web/trust.test.mjs`
- Modify: `web/embed.go:13` (add `trust.js` to `//go:embed`)
- Modify: `web/manifest_test.go:13` (add `"trust.js"` to the names)
- Modify: `.gitlab-ci.yml:34`, `.github/workflows/ci.yml:19` (add `web/trust.test.mjs` to `node --test`)

**Interfaces:**
- Produces:
  - `trustState({ ended, keyError, alone, hasInvite, code, verified }) -> 'hidden' | 'broken' | 'invite' | 'waiting' | 'verify' | 'verified' | 'later'`
    Precedence: `ended` → `'hidden'`; `keyError` → `'broken'`; `alone && hasInvite` → `'invite'`; `alone` → `'waiting'`; `!code` → `'hidden'` (the key is still being derived); `verified === 'match'` → `'verified'`; `verified === 'later'` → `'later'`; otherwise `'verify'`.
  - `badge(state) -> { label: string, tone: 'ok' | 'warn' | 'danger' } | null`: `verify`/`later` → `{ 'Not verified', 'warn' }`; `verified` → `{ '✓ Verified', 'ok' }`; `broken` → `{ 'Key error', 'danger' }`; others → `null`.
  - `collapsible(state) -> boolean`: true only for `'verified'` and `'later'`.
  - `inviteURL(origin, invite, secret, hasPassword) -> string`: the same format as `app.js:153` today, `${origin}/#invite=${invite}&k=${secret}` plus `&pw=1` when `hasPassword`.
  - `codeLabel(code) -> string`: `'1084 2945 3921'` → `'Safety code: 1 0 8 4, 2 9 4 5, 3 9 2 1'`.

- [ ] **Step 1: Write the failing tests** in `web/trust.test.mjs` (style of `linkify.test.mjs`):
  - `ended wins over everything`: `trustState({ ended: true, keyError: true, alone: true, hasInvite: true })` is `'hidden'`.
  - `key error wins over verification`: `{ keyError: true, code: '1', verified: 'match' }` gives `'broken'`.
  - `alone with invite shows invite, without shows waiting`: `'invite'` and `'waiting'`.
  - `two people, no code yet, stays hidden`: `{ alone: false, code: '' }` gives `'hidden'`.
  - `verified values map to states`: undefined → `'verify'`, `'match'` → `'verified'`, `'later'` → `'later'`.
  - `badge per state`: the exact label/tone pairs above, and `null` for `invite`, `waiting` and `hidden`.
  - `collapsible only after an answer`: true for `verified` and `later`, false for the rest.
  - `invite URL keeps today's format`: `inviteURL('https://x.io', 'tok', 'sec', true)` is `'https://x.io/#invite=tok&k=sec&pw=1'`, and with `false` there is no `&pw=1`.
  - `code label spells digits`: the example above.

- [ ] **Step 2: Run to verify it fails**
  Run: `node --test web/trust.test.mjs`
  Expected: FAIL, `Cannot find module './trust.js'`.

- [ ] **Step 3: Implement `web/trust.js`** with the five exports above; no DOM access. Add it to `embed.go`, `manifest_test.go` and both CI files.

- [ ] **Step 4: Run to verify it passes**
  Run: `node --test web/*.test.mjs && go test ./web/`
  Expected: all pass.

- [ ] **Step 5: Commit** `feat(web): pure trust-state module for the invite and safety-code strip`

---

### Task 2: End-chat dialog replaces confirm()

**Files:**
- Modify: `web/index.html` (add `<dialog id="end-dialog" aria-labelledby="end-dialog-text">` at the end of `#chat`: `<p id="end-dialog-text">`, `<button id="end-confirm" class="danger">End for both</button>`, `<button id="end-cancel" class="secondary">Cancel</button>`)
- Modify: `web/app.js:583-588` (End handler), `web/app.js:342` (`finish`)
- Modify: `web/style.css` (dialog and `::backdrop`, using the existing tokens)
- Test: `web/app_test.go`

**Interfaces:**
- Produces: `openEndDialog(reason: 'end' | 'mismatch' | 'broken', returnTo: HTMLElement) -> void`. It sets the text from a `END_COPY` map (the spec copy), calls `showModal()`, and returns focus to `returnTo` on close. Confirm runs the current end flow: `api('/api/v1/end', …)` then `finish('Conversation ended')`, with today's error handling.

- [ ] **Step 1: Write the failing test** `TestFrontendEndsChatThroughDialog`:
  - `app.js` does not contain `confirm(`;
  - `index.html` contains `<dialog id="end-dialog"`;
  - `app.js` contains `function openEndDialog(` and `.showModal()`;
  - the body of `function finish(` contains `$('end-dialog').close()` (Review Focus 4);
  - `END_COPY` contains the three spec strings verbatim.

- [ ] **Step 2: Run to verify it fails**
  Run: `go test ./web/ -run TestFrontendEndsChatThroughDialog`
  Expected: FAIL on `confirm(`.

- [ ] **Step 3: Implement.** The sidebar End button calls `openEndDialog('end', $('end-button'))`. Esc and Cancel close the dialog without action (native `cancel` event). In `finish()`, close the dialog if it is open.

- [ ] **Step 4: Run to verify it passes**
  Run: `go test ./web/ && node --check web/app.js`
  Expected: PASS.

- [ ] **Step 5: Commit** `feat(web): confirm ending the chat in a dialog instead of confirm()`

---

### Task 3: Strip shell, invite and waiting states

**Files:**
- Modify: `web/index.html`:
  - after `.conversation-head`: `<section id="trust-strip" class="trust-strip" aria-labelledby="trust-title" hidden><h3 id="trust-title" tabindex="-1"></h3><div id="trust-body"></div><p id="trust-status" class="sr-only" role="status"></p></section>`;
  - in `.conversation-head`: `<button id="trust-badge" class="trust-badge" type="button" aria-controls="trust-strip" aria-expanded="false" hidden></button>`;
  - remove `#share-panel`.
- Modify: `web/app.js`:
  - import from `./trust.js`;
  - add `renderTrust()`;
  - `renderParticipants()` (lines 152-153) calls `renderTrust()` instead of touching `share-panel`;
  - the copy handler (lines 578-582) moves into the strip's Copy button;
  - remove the `#invite-link` focus listener (line 658);
  - the `invite_revoked` handler (line 460) stops calling `showError('chat-error', …)`;
  - `finish()` calls `renderTrust()`.
- Modify: `web/style.css` (`.trust-strip`, `.trust-link`, `.trust-badge` with tones; remove the `.share-panel` rules at lines 488-500 and 953-963)
- Modify: `web/app.js:204` (the empty-state copy for `alone` becomes "Your contact has not joined yet." because the strip now carries the instruction)
- Test: `web/app_test.go`

**Interfaces:**
- Consumes: `trustState`, `badge`, `collapsible`, `inviteURL` from Task 1.
- Produces: `renderTrust() -> void`, which reads `state.ended`, `state.keyError`, `state.room.participants.length === 1`, `state.invite && state.e2ee?.secret`, `state.safetyCode` and `state.e2ee?.verified`. It also uses `state.trustOpen: boolean`, which says whether a collapsible state is expanded (default `false`). It rebuilds `#trust-body` only when the derived state changes (`state.trustShown`), so re-renders don't wipe focus or the copy feedback.

- [ ] **Step 1: Write the failing test** `TestFrontendInviteLivesInTrustStrip`:
  - `index.html` has no `id="share-panel"` and has `id="trust-strip"`, `id="trust-title" tabindex="-1"`, `id="trust-badge"` with `aria-controls="trust-strip"`, and `id="trust-status"` with `role="status"`;
  - `#trust-strip` is not inside `id="messages"`: its index is outside the `messages-wrap` substring;
  - `app.js` contains `from './trust.js'`, `navigator.share` and `navigator.clipboard.writeText`;
  - the `invite_revoked` branch no longer contains `showError('chat-error'` (Review Focus 5);
  - the spec copy strings for the invite and waiting states appear verbatim in `app.js`.

- [ ] **Step 2: Run to verify it fails**
  Run: `go test ./web/ -run TestFrontendInviteLivesInTrustStrip`
  Expected: FAIL, `share-panel` is still present.

- [ ] **Step 3: Implement the `invite` and `waiting` branches of `renderTrust()`.**
  - The link is a `<p class="trust-link">`: monospace, `overflow-wrap:anywhere`, `user-select:all`.
  - "Share…" is rendered only when `typeof navigator.share === 'function'`. It calls `navigator.share({ url })` and ignores `AbortError`.
  - The password line appears only when `state.e2ee.has_password` is set.
  - `waiting` uses the danger tone.

- [ ] **Step 4: Run to verify it passes**
  Run: `go test ./web/ && node --check web/app.js && node --test web/*.test.mjs`
  Expected: PASS.

- [ ] **Step 5: Commit** `feat(web): invite link in a pinned strip above the conversation`

---

### Task 4: Verify, verified, later and broken states with the badge

**Files:**
- Modify: `web/app.js`:
  - `establishKey()` (line 130) stores the code in `state.safetyCode` and calls `renderTrust()`;
  - `keyFailure()` (lines 97-106) keeps writing `#safety-alert` and calls `renderTrust()`;
  - add `setVerified(value: 'match' | 'later' | undefined)`;
  - add the badge click handler;
  - `renderTrust()` gets the `verify`, `verified`, `later` and `broken` branches.
- Modify: `web/index.html`:
  - `#safety-panel` shrinks to `<section id="safety-panel" class="safety-panel"><span class="eyebrow">ENCRYPTION</span><p>End-to-end encrypted in your browser.</p></section>`;
  - `<p id="safety-alert" class="sr-only" role="alert"></p>` moves, with the same markup, into `#trust-strip`.
- Modify: `web/style.css`:
  - `.trust-code`: at least 24px, monospace, keep `letter-spacing:.08em` as today;
  - remove the now-unused `.safety-panel strong` / `.broken` rules.
- Test: `web/app_test.go` (update `TestFrontendKeepsChatUsableWithKeyboardAndScreenReader`: it reads `#safety-code`; point it at the new structure)

**Interfaces:**
- Consumes: Task 1 exports, Task 2's `openEndDialog`, Task 3's `renderTrust` and `state.trustOpen`.
- Produces: `setVerified(value) -> void`. It writes `state.e2ee.verified` and calls `save()`, then sets `state.trustOpen = false` and calls `renderTrust()`. It then moves focus to `#trust-badge` and writes "Marked as verified." to `#trust-status` when `value === 'match'`.

- [ ] **Step 1: Write the failing test** `TestFrontendVerifiesSafetyCodeLocally`:
  - `app.js` contains `function setVerified(`, and its body writes `state.e2ee.verified` and calls `save()` (Review Focus 1);
  - the "It doesn't match" handler calls `openEndDialog('mismatch'`, and no `setVerified` call sits in the `openEndDialog` body or in the mismatch handler (Review Focus 3);
  - the broken state's button calls `openEndDialog('broken'`;
  - `renderTrust` contains `contains(document.activeElement)` and `$('trust-title').focus()` (Review Focus 2);
  - `renderTrust` sets `aria-label` from `codeLabel(`;
  - `index.html` still contains `<p id="safety-alert" class="sr-only" role="alert"></p>`, now inside the `trust-strip` substring;
  - no new WebSocket message type: `app.js` does not contain `verified` inside any `send(` / `transmit(` call.

- [ ] **Step 2: Run to verify it fails**
  Run: `go test ./web/ -run TestFrontendVerifiesSafetyCodeLocally`
  Expected: FAIL, `setVerified` is missing.

- [ ] **Step 3: Implement the four branches and the badge.**
  - `verify`: the heading, the code as `<strong class="trust-code">` with `aria-label`, the text, and the three buttons. "Later" and a badge click both call `setVerified('later')`.
  - `verified` when expanded: the code, the confirmation line, "Compare again" (`setVerified(undefined)` then `state.trustOpen = true`) and "Hide".
  - `later` when expanded: the `verify` content.
  - `broken`: the heading, the note from `keyFailure()` and "End the chat". It is never collapsible; a badge click focuses `#trust-title`.
  - Badge: `hidden` when `badge(state)` is `null`; otherwise its label, `data-tone` and `aria-expanded` (the strip's visible state).
  - On the transition into `verify`, write "Your contact joined. Compare the safety code." to `#trust-status`.

- [ ] **Step 4: Run to verify it passes**
  Run: `go test ./web/ && node --check web/app.js && node --test web/*.test.mjs`
  Expected: PASS.

- [ ] **Step 5: Commit** `feat(web): safety-code check with a local verified badge`

---

### Task 5: Browser verification, docs and detector

**Files:**
- Modify: `docs/UI_TEST_CASES.md` (cases for each strip state, the badge, reload, the dialog paths and focus on join)
- Modify: `SPEC.md:17` (one sentence: both participants can mark the code as matching; the mark is local to the browser and is not sent)

- [ ] **Step 1: Rebuild and run the app.** The container serves embedded assets, so rebuild with `docker compose up --build -d`, then open the `nullchat` preview.

- [ ] **Step 2: Walk the states in two separate tabs.**
  - `invite` with and without a password; Copy; Share appears only where supported.
  - The guest joins while focus is on Copy (focus goes to the heading). `verify` on both sides.
  - "It matches", then reload: the badge is still "✓ Verified". "Compare again".
  - "Later", then reload.
  - "It doesn't match", then Cancel: still `verify`. Again, then confirm: the chat ends.
  - The peer ends the chat while the dialog is open: the dialog closes.
  - Mobile width (375): the strip sits at the top of the conversation and takes no space when collapsed.
  - Expected: every case behaves as the spec says; no console errors.

- [ ] **Step 3: Run the design detector** on the changed markup:
  `"/Users/fnder77/.claude/plugins/cache/impeccable/impeccable/4.10.0/skills/impeccable/scripts/impeccable" detect --json --changed`
  Expected: no new `undersized-ui-text` / `tiny-text` findings in `#trust-strip`. Pre-existing findings elsewhere are out of scope.

- [ ] **Step 4: Full test run**
  Run: `go test -race ./... && node --check web/app.js && node --test web/*.test.mjs`
  Expected: PASS.

- [ ] **Step 5: Commit, push and open an MR** to `main`: `docs: UI test cases for the trust strip`, then `git push -u origin feat/trust-strip` and open the MR. Never merge locally.
