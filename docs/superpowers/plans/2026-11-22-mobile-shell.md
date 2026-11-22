# Mobile Chat Shell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** At ≤ 700px the chat is one fixed viewport: a compact head (title, timer chip, ⋯), the status row, the message list as the only scroller, and the composer. The sidebar becomes a bottom sheet opened by ⋯.

**Architecture:** Markup gains a mobile-only `.chat-head`. The existing `.room-sidebar` is styled as a fixed sheet under the mobile query, and small JS helpers open and close it. Title and timer are mirrored into the head. A `visualViewport` listener keeps the shell sized to the visible area on iOS. Desktop CSS and DOM order are untouched.

**Tech Stack:** Vanilla JS/HTML/CSS; Go string-contract tests in `web/app_test.go`; `node --test` for existing pure modules.

**Spec:** `docs/superpowers/specs/2026-11-22-mobile-shell-design.md`

## Global Constraints

- Mobile means `@media(max-width:700px)`. In JS, use one `const MOBILE = matchMedia('(max-width:700px)')`. Above 700px the layout and behaviour do not change.
- No `innerHTML`. Use existing tokens only. All text on the mobile chat screen is at least 12px. Contrast is at least 4.5:1.
- The sidebar DOM is single-sourced: the sheet *is* `.room-sidebar`, given `id="room-sheet"`. Only the title and the timer string are mirrored.
- Ids and copy, from the spec:
  - `#chat-head-title` (`h2`, `tabindex="-1"`)
  - `#chat-head-timer` (`span.timer-chip`, `aria-hidden="true"`)
  - `#sheet-button` (`aria-controls="room-sheet"`, `aria-expanded`, `aria-label="Room details"`, text "⋯")
  - `#sheet-close` (`aria-label="Close"`, text "×")
  - `#sheet-backdrop`
  - body class `in-chat`
  - CSS custom property `--app-height`
  - the ended chip reads "Ended"
- `#timer-alert` (role=status) is **moved** out of `.timer-card` to right after `.chat-head`. It is not duplicated.
- The closed sheet is `display:none` on mobile. While it is open, the sheet has `role="dialog"`, `aria-modal="true"` and `aria-label="Room details"`, and `.chat-head` and `.conversation` are `inert`.
- `<meta name="viewport" content="width=device-width, initial-scale=1, interactive-widget=resizes-content">`.

## Review Focus

1. **Rotation or resize past 700px while the sheet is open.** The sheet closes, `inert` and the dialog attributes are removed, and the desktop sidebar is fully usable. Pinned in Task 2.
2. **Cancel in the end dialog opened from inside the sheet.** Focus returns to `#end-button` while the sheet stays open, and the sheet's `inert` state stays unchanged. Pinned in Task 2.
3. **The room ends** (peer ends it or TTL expires) **while the sheet is open.** The sheet closes, ⋯ hides, and the chip reads "Ended". Pinned in Task 2.
4. **The composer is focused while the trust strip is open in verify.** The strip hides, the badge stays, and on blur it returns. `state.e2ee.verified` and `state.trustOpen` don't change. Pinned in Task 3.
5. **A restore failure returns to home.** `in-chat` is removed, so the site header and footer show again. Pinned in Task 1.

---

### Task 1: Shell layout, head and mirroring

**Files:**
- Modify: `web/index.html`:
  - add `.chat-head` as the first child of `<main id="chat">`, with `#chat-head-title`, `#chat-head-timer` and `#sheet-button`;
  - move `<p id="timer-alert" class="sr-only" role="status"></p>` to right after `.chat-head`;
  - add `id="room-sheet"` to `.room-sidebar`;
  - update the viewport meta.
- Modify: `web/app.js`:
  - `enter()`: add `in-chat` to body, set `#chat-head-title`, and focus `#chat-head-title` if `MOBILE.matches`, else `#room-title`;
  - `updateTimer()`: mirror the string, and `.ending` onto `#chat-head-timer`;
  - `finish()`: chip text "Ended", remove `.ending` from both, hide `#sheet-button`;
  - `restoreFailed`: remove `in-chat`.
- Modify: `web/style.css`, inside the mobile chat block:
  - `body.in-chat .header` and `body.in-chat footer` get `display:none`;
  - `#chat` becomes a fixed flex column (`position:fixed; left:0; right:0; top:0; height:var(--app-height,100dvh)`) with the shell padding;
  - `.conversation` gets `flex:1; min-height:0`, and the old `calc(100dvh - 245px)` and `min-height:430px` are dropped at this width;
  - `.messages-wrap` gets `flex:1; min-height:0`;
  - `.chat-head` and `.timer-chip` are styled; the closed sheet (`.room-sidebar`) is `display:none` for now (Task 2 opens it);
  - every font-size below 12px inside the mobile chat is raised to 12px;
  - `.chat-head` is hidden above 700px.
- Test: `web/app_test.go`

**Interfaces:**
- Produces:
  - `const MOBILE` (MediaQueryList) at module top in `app.js`.
  - Mirrored ids (`#chat-head-title`, `#chat-head-timer`), as above.

- [ ] **Step 1: Write the failing test** `TestFrontendMobileShellLayout`. It asserts:
  - **index.html:**
    - `<header class="chat-head">` appears inside `<main id="chat"` before `room-sidebar`;
    - `id="chat-head-title" tabindex="-1"`;
    - `id="chat-head-timer"` with `aria-hidden="true"`;
    - `id="sheet-button"` with `aria-controls="room-sheet"` and `aria-label="Room details"`;
    - `<p id="timer-alert" class="sr-only" role="status"></p>` appears outside the `timer-card` substring;
    - `interactive-widget=resizes-content`.
  - **app.js:**
    - `matchMedia('(max-width:700px)')`;
    - the `enter(` body contains `classList.add('in-chat')` and `$('chat-head-title').focus()`, and still contains `$('room-title').focus()`;
    - the `updateTimer(` body writes `chat-head-timer`;
    - the `finish(` body contains `'Ended'` and hides `sheet-button`;
    - the restore-failure path contains `classList.remove('in-chat')` (Review Focus 5).
  - **style.css:** in a `max-width:700px` block, `.messages-wrap` has `min-height:0`, and `#chat` uses `var(--app-height`.
  - Keep the existing focus assertions in `TestFrontendKeepsChatUsableWithKeyboardAndScreenReader` passing. A minimal edit is allowed if both targets stay pinned.
- [ ] **Step 2: Run to verify it fails.** Run `go test ./web/ -run TestFrontendMobileShellLayout`. Expected: FAIL.
- [ ] **Step 3: Implement** the markup, the JS mirroring and lifecycle, and the CSS above.
- [ ] **Step 4: Run to verify it passes.** Run `go test ./web/ && node --check web/app.js && node --test web/*.test.mjs`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): fixed mobile chat shell with a compact head`

---

### Task 2: Sidebar as a bottom sheet

**Files:**
- Modify: `web/index.html`: add `<button id="sheet-close" type="button" aria-label="Close">×</button>` as the first child of `.room-sidebar`, and `<div id="sheet-backdrop" hidden></div>` inside `#chat`.
- Modify: `web/app.js`: add `openSheet()` and `closeSheet({ restoreFocus = true } = {})`, the listeners on ⋯, close, backdrop and Esc, and an `MOBILE` change listener. `finish()` calls `closeSheet({ restoreFocus: false })`.
- Modify: `web/style.css`, mobile block:
  - `#sheet-close` and `#sheet-backdrop` are hidden above 700px;
  - `.room-sidebar.open` is a fixed bottom sheet (`inset:auto 0 0 0; max-height:85%; overflow-y:auto`) on panel background with a top border and radius. It slides in via `transform` over ≤ 200ms, with no transition under `prefers-reduced-motion`;
  - `.timer-card` is hidden inside the sheet, and `#room-title` is visible in full.
- Test: `web/app_test.go`

**Interfaces:**
- Consumes: `MOBILE` and the ids from Task 1.
- Produces:
  - `openSheet() -> void`:
    - adds `.open` to `#room-sheet` and shows `#sheet-backdrop`;
    - sets `role="dialog"`, `aria-modal="true"` and `aria-label="Room details"` on `#room-sheet`;
    - sets `#sheet-button` `aria-expanded="true"`;
    - sets `.chat-head` and `.conversation` `inert = true`;
    - focuses `#sheet-close`.
  - `closeSheet({ restoreFocus }) -> void`:
    - reverses all of the above, removing the dialog attributes and `inert`;
    - focuses `#sheet-button` when `restoreFocus`;
    - is a no-op when the sheet is not open.

- [ ] **Step 1: Write the failing test** `TestFrontendMobileSheet`. It asserts:
  - `function openSheet(` sets `aria-modal`, `inert = true` and `$('sheet-close').focus()`;
  - `function closeSheet(` removes `aria-modal` and `role`, sets `inert = false`, and focuses `sheet-button` only under `restoreFocus`;
  - a `keydown` listener handles `'Escape'` by calling `closeSheet(`;
  - `sheet-backdrop` has a click listener that calls `closeSheet(`;
  - `MOBILE.addEventListener('change'` calls `closeSheet({ restoreFocus: false })` when the query stops matching (Review Focus 1);
  - the `finish(` body calls `closeSheet({ restoreFocus: false })` (Review Focus 3);
  - the end dialog's close handler is unchanged, so focus returns to `endReturnTo`, and `closeSheet` is not called from `openEndDialog` (Review Focus 2);
  - the CSS has a `prefers-reduced-motion` rule for `.room-sidebar`.
- [ ] **Step 2: Run to verify it fails.** Run `go test ./web/ -run TestFrontendMobileSheet`. Expected: FAIL.
- [ ] **Step 3: Implement** the helpers, listeners and CSS.
- [ ] **Step 4: Run to verify it passes.** Run `go test ./web/ && node --check web/app.js && node --test web/*.test.mjs`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): room details as a bottom sheet on mobile`

---

### Task 3: Keyboard handling

**Files:**
- Modify: `web/app.js`:
  - add `syncViewport() -> void`;
  - add `visualViewport` `resize` and `scroll` listeners, active only while `#chat` is visible and `MOBILE.matches`;
  - add `focusin` and `focusout` handling on `#message` that toggles `state.composerFocus`;
  - `renderTrust()` hides the strip when `MOBILE.matches && state.composerFocus` and the derived state is `verify`, `verified` or `later`;
  - when the user was at the bottom of the list before a viewport resize, keep them at the bottom.
- Test: `web/app_test.go`

**Interfaces:**
- Consumes: `MOBILE` (Task 1); `renderTrust()` and `trustState` (existing).
- Produces:
  - `syncViewport()`. It sets `#chat`'s `--app-height` to `${visualViewport.height}px` and `style.top` to `${visualViewport.offsetTop}px`. When the chat is hidden or the query does not match, it clears both.
  - `state.composerFocus: boolean`.

- [ ] **Step 1: Write the failing test** `TestFrontendMobileKeyboard`. It asserts:
  - `function syncViewport(` reads `visualViewport.height` and `visualViewport.offsetTop`, and sets `--app-height`;
  - `visualViewport.addEventListener('resize'` and `visualViewport.addEventListener('scroll'` are present;
  - `syncViewport` clears the property via `removeProperty('--app-height')`;
  - the `renderTrust(` body references `state.composerFocus` and `MOBILE.matches` (Review Focus 4);
  - no code in the focus handlers assigns `state.e2ee.verified` or `state.trustOpen` (Review Focus 4).
- [ ] **Step 2: Run to verify it fails.** Run `go test ./web/ -run TestFrontendMobileKeyboard`. Expected: FAIL.
- [ ] **Step 3: Implement.** Guard every `visualViewport` use with `globalThis.visualViewport &&`.
- [ ] **Step 4: Run to verify it passes.** Run `go test ./web/ && node --check web/app.js && node --test web/*.test.mjs`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): keep the mobile composer above the keyboard`

---

### Task 4: Browser verification and docs

**Files:**
- Modify: `docs/UI_TEST_CASES.md` (cases MOBILE-01… for each goal and each Review Focus item)

- [ ] **Step 1: Rebuild and check the served file.** Run `docker compose up --build -d`, then confirm the served `/app.js` and `/style.css` shasums match `web/`.
- [ ] **Step 2: Walk the mobile layout in new tabs at 375×812 and 320×700, using two participants.** Check each of these:
  - Only `.messages` scrolls, `document.scrollingElement.scrollHeight === innerHeight`, and the head and composer stay put while 30 messages scroll.
  - ⋯ opens the sheet with focus on ×. Esc, ×, and a backdrop tap each close it, and focus returns to ⋯.
  - End from the sheet, then Cancel, puts focus on `#end-button`. End then confirm ends the room.
  - The peer ends the room while the sheet is open: the sheet closes and the chip reads "Ended".
  - With the composer focused in verify, the strip hides and the badge stays.
  - Resizing to desktop while the sheet is open closes it.
  - All computed font-sizes inside `#chat` are at least 12px.
  - Desktop at 1280 is unchanged.

  Expected: every case passes, with no console errors.
- [ ] **Step 3: Check the iOS keyboard.** If `xcrun simctl list devices available` shows an iPhone, run that check in the iOS Simulator. Otherwise record "iOS keyboard not verified: no simulator on this machine".
- [ ] **Step 4: Run the detector and the full test suite.**
  - Run `"/Users/fnder77/.claude/plugins/cache/impeccable/impeccable/4.10.0/skills/impeccable/scripts/impeccable" detect --json web/index.html`. Expected: no findings inside `#chat`.
  - Run `go test -race ./... && node --check web/app.js && node --test web/*.test.mjs`. Expected: PASS.
- [ ] **Step 5: Commit** `docs: UI test cases for the mobile chat shell`. Do not push.
