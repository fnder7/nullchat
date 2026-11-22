# Polish 2/3: Sidebar and Sheet Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the room sidebar (desktop) and the room sheet (mobile) clean:
- notifications become one line;
- the reload note is removed;
- the participants section gets a plain heading and a status line;
- the message counter appears only near the limit;
- End becomes a real danger button;
- the YOUR SPACE eyebrow is removed;
- chat and sidebar text are at least 12px on desktop.

**Architecture:** Markup and CSS changes in `web/index.html`/`web/style.css`, with small render changes in `web/app.js` (`renderNotify`, `renderParticipants`, `renderMessages`). A Go test scans `style.css` for sub-12px sizes and allows only home-only selectors, which MR 3 removes.

**Tech Stack:** Vanilla HTML/CSS/JS, Go string-contract tests.

**Spec:** `docs/superpowers/specs/2026-11-22-polish-design.md` (section "MR 2")

## Global Constraints

- Branch `feat/polish-sheet` from `main` after MR 1 is merged and deployed. One MR.
- The desktop sidebar and the mobile sheet are the same DOM (`#room-sheet`); every change applies to both.
- Copy:
  - "Notifications are blocked in this browser." (when denied or unsupported, shown as one muted line);
  - "Participants" (heading);
  - "Waiting for your contact" (status, only while alone);
  - "N / 500 messages" (only when N ≥ 450);
  - "End the chat for both".
- Fonts: the text face plus `var(--mono)` from MR 1. Text at least 12px; contrast at least 4.5:1.
- No `innerHTML`.

## Review Focus

1. **Notifications unsupported (e.g. iOS Safari without a home-screen install).** Today the panel is hidden; with this change it shows the blocked line instead, so the user knows why there is no toggle. Pinned in Task 1.
2. **The contact joins while the sheet is open.** The status line is cleared and the change is announced politely, without moving focus. Pinned in Task 2.
3. **A room hits 450 messages.** The counter appears in the composer hint row, and on mobile it stays one line. Pinned in Task 2.
4. **After the end.** The End button stays hidden, as today, and the danger style does not resurrect it. Pinned in Task 1.
5. **Desktop at 1050px (tablet layout, 220px sidebar).** The full-width End button and the heading fit without overflow. Measured in Task 4.

---

### Task 1: Notifications, reload note, End button

**Files:** Modify `web/index.html` (`#notify-panel`, `.sidebar-note`, `#end-button`), `web/app.js` (`renderNotify`), `web/style.css`. Test: `web/app_test.go`.

**Interfaces:**
- Produces: `renderNotify()` shows `#notify-panel` whenever the room is open.
  - If `notifySupported()` is false or the permission is `'denied'`, it hides `#notify-button` and sets `#notify-note` to "Notifications are blocked in this browser."
  - Otherwise it keeps today's toggle and note.

- [ ] **Step 1: Write the failing test** `TestFrontendSheetNotificationsAndEnd`. It asserts:
  - **The reload note is gone.** `index.html` has no `class="sidebar-note"`.
  - **End is a danger button.** `index.html` has `<button id="end-button" class="danger end-button" type="button">End the chat for both</button>`.
  - **The blocked line.** The `renderNotify` body contains `'Notifications are blocked in this browser.'` and `$('notify-button').hidden =`, and does not set `.disabled = denied` (Review Focus 1).
  - **The button styles.** In `style.css`, `.end-button` has `width:100%` and a `margin-top` of at least 24px. `.notify-panel` no longer styles a disabled button.
  - **Hidden after the end.** The `finish(` body still hides `end-button` (Review Focus 4).
- [ ] **Step 2: Run to verify it fails.** Run `go test ./web/ -run TestFrontendSheetNotificationsAndEnd`. Expected: FAIL.
- [ ] **Step 3: Implement.** Remove the `.sidebar-note` and `.danger-link` CSS that becomes unused. Keep the `.danger` base from the end dialog.
- [ ] **Step 4: Run to verify it passes.** Run the full pre-commit line. Expected: PASS.
- [ ] **Step 5: Commit** `fix(web): one-line notification state, no reload note, End as a danger button`

---

### Task 2: Participants heading and status, message counter

**Files:** Modify `web/index.html` (`.participants-heading`, the `YOUR SPACE` eyebrow), `web/app.js` (`renderParticipants` near the `participant-count` write; `renderMessages` near the `message-count` write), `web/style.css`. Test: `web/app_test.go`.

**Interfaces:**
- Produces: `#participants-status` (`<p id="participants-status" class="participants-status" role="status">`), which replaces `#participant-count`.
  - `renderParticipants` sets it to "Waiting for your contact" when there is one participant and the room is open, and to `''` otherwise.
  - `#message-count` is hidden unless `messages.length >= 450`.

- [ ] **Step 1: Write the failing test** `TestFrontendSheetParticipantsAndCounter`. It asserts:
  - **index.html:**
    - has `<h3 class="participants-title">Participants</h3>`;
    - has `id="participants-status"` with `role="status"`;
    - has no `id="participant-count"`;
    - has no `>YOUR SPACE<`;
    - has no `>PARTICIPANTS<`.
  - **app.js:**
    - the `renderParticipants` body contains `'Waiting for your contact'`;
    - the `renderParticipants` body does not contain `.focus(` (Review Focus 2);
    - the `renderMessages` body contains `>= 450` and toggles `$('message-count').hidden` (Review Focus 3).
- [ ] **Step 2: Run to verify it fails.** Run `go test ./web/ -run TestFrontendSheetParticipantsAndCounter`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Style `.participants-title` as a 14px semibold text-face heading, in the same style as the encryption section heading's size.
  - Style `.participants-status` as 13px muted text.
  - Update existing tests that pin the old markup or copy.
- [ ] **Step 4: Run to verify it passes.** Run the full pre-commit line. Expected: PASS.
- [ ] **Step 5: Commit** `fix(web): plain participants heading with a waiting status; counter only near the limit`

---

### Task 3: 12px floor in chat and sidebar on desktop

**Files:** Modify `web/style.css`. Test: `web/app_test.go`.

**Interfaces:**
- Produces: a test helper `smallFontRules(css string) []string`. It returns every rule selector whose declarations set `font-size` or the `font` shorthand below 12px. Task 1 of the next plan reuses it.

- [ ] **Step 1: Write the failing test** `TestFrontendNoSmallTextOutsideHome`.
  - It calls `smallFontRules` on all of `style.css`, the media blocks included.
  - It fails for any selector not in an explicit `homeOnly` allowlist: home and footer selectors only, such as `.principle-num`, `.flow`, `.room-facts`, `footer`, the home `.eyebrow`, `.small-print` and `.card-footnote`. The implementer lists the exact selectors found.
  - It must fail today on the chat selectors, for example `.composer-hint`, `.message-meta`, `.conversation-label`, `.timer-card>span`, `.avatar` and `.participants-heading>span:last-child`.
- [ ] **Step 2: Run to verify it fails.** Run `go test ./web/ -run TestFrontendNoSmallTextOutsideHome`. Expected: FAIL, listing the chat selectors.
- [ ] **Step 3: Implement.** Raise every non-home rule to at least 12px. Remove rules that became dead in Tasks 1–2 instead of resizing them. Check that the tablet sidebar (220px) still fits.
- [ ] **Step 4: Run to verify it passes.** Run the full pre-commit line. Expected: PASS.
- [ ] **Step 5: Commit** `fix(web): chat and sidebar text at least 12px on desktop`

---

### Task 4: Browser and iOS verification, docs, ship

- [ ] **Step 1: Rebuild and check what is served.** Run `docker compose up --build -d`, then confirm the served files match `web/`.
- [ ] **Step 2: Walk in new tabs** (built-in browser, 1280, 1050 and 375; two participants):
  - **Notifications.** The line shows when the permission is denied.
  - **Waiting status.** It shows while alone, then clears and is announced when the guest joins, with the sheet open on mobile.
  - **Message counter.** Absent below 450. To check it at 450, set `state` in the console or send messages with a loop.
  - **End button.** Full width; the dialog still works.
  - **12px floor.** The minimum computed font-size inside `#chat` is at least 12px at 1280 and 1050.
  - **Fit at 1050.** No overflow there (Review Focus 5).
- [ ] **Step 3: Check the sheet in the iOS Simulator.**
- [ ] **Step 4: Run the full suite and the detector.** Run the full suite, then the detector. There must be no `kicker-above-heading` finding inside `#chat`.
- [ ] **Step 5: Update docs and ship.** Update `docs/UI_TEST_CASES.md` (cases SHEET-01…) and commit. The controller then pushes and opens the MR with auto-merge, and waits for the deploy.
