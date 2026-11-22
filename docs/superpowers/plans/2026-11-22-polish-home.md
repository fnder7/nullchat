# Polish 3/3: Home and Guest Screen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clean up the home page and the guest screen:
- headings no tighter than -0.04em;
- no eyebrow labels or principle numbers;
- all text at least 12px;
- a footer without the healthz link or repeated claims;
- a default title that isn't the input's placeholder.

**Architecture:** Markup and CSS changes in `web/index.html` and `web/style.css`, plus a constant change in `web/app.js`. The `homeOnly` allowlist from plan 2's `TestFrontendNoSmallTextOutsideHome` is emptied, so the whole stylesheet has no text below 12px.

**Tech Stack:** Vanilla HTML/CSS/JS, Go string-contract tests.

**Spec:** `docs/superpowers/specs/2026-11-22-polish-design.md` (section "MR 3")

## Global Constraints

- Branch `feat/polish-home` from `main` after MR 2 is merged and deployed. One MR.
- **Copy:**
  - footer: "nullchat © 2026" and "History lasts only until the room closes.";
  - title placeholder: "e.g. Weekend plans";
  - default title: "Private conversation".
- **Tracking:** every `letter-spacing` is an em value no tighter than `-0.04em`.
- **Keep:** the 01/02/03 flow numbers stay, because they are a sequence.
- **Remove:** the "A PRIVATE SPACE, FOR A WHILE" and "NEW CONVERSATION" eyebrows, and the `/ 01 / 02 / 03` principle numbers.
- No `innerHTML`.

## Review Focus

1. **The guest heading at 320px.** "You're invited to a private conversation" wraps cleanly with no touching letters. Measured in Task 3, with a CSS assertion in Task 1.
2. **A room created with an empty title.** It shows "Private conversation" in the head, the sheet and the tab title. A user who types "Quiet conversation" sees exactly that. Pinned in Task 2.
3. **The home page at 375px.** With the eyebrow gone, the hero still starts below the header with sensible space, and the create button is no further down than today. Measured in Task 3.
4. **The footer on mobile.** After removing two items, the remaining two items don't leave an awkward gap. Measured in Task 3.
5. **The detector.** After this MR it reports no `tiny-text`, `undersized-ui-text`, `hero-eyebrow-chip` or `kicker-above-heading` on `web/index.html`. Checked in Task 3.

---

### Task 1: Tracking, labels and the 12px floor on home

**Files:** Modify `web/index.html` (intro eyebrow, card-top eyebrow, principle numbers), `web/style.css`, `web/app_test.go` (empty the `homeOnly` allowlist).

**Interfaces:** Consumes `smallFontRules` from plan 2, Task 3.

- [ ] **Step 1: Write the failing tests.**
  - `TestFrontendNoSmallTextOutsideHome` with `homeOnly` emptied. It fails on the home selectors.
  - `TestFrontendHomeHasNoEyebrows`. It asserts:
    - `index.html` has no `class="eyebrow"`, no `principle-num` and no `card-eyebrow`;
    - every `letter-spacing:` value in `style.css` uses `em` units and is ≥ -0.04 (regex over all values; Review Focus 1).
  - Update tests that pin `card-eyebrow` (the `renderInvite` / guest code may reference it). Remove those references in `app.js`.
- [ ] **Step 2: Run to verify they fail.** Run `go test ./web/ -run 'TestFrontendNoSmallTextOutsideHome|TestFrontendHomeHasNoEyebrows'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Convert px tracking to em with the floor.
  - Raise home text to at least 12px.
  - Remove the eyebrow and principle-number markup and their CSS. If no `.eyebrow` use remains anywhere, remove the `.eyebrow` rule too.
- [ ] **Step 4: Run to verify they pass.** Run the full pre-commit line. Expected: PASS.
- [ ] **Step 5: Commit** `fix(web): home without eyebrow labels or tiny text; tracking floor -0.04em`

---

### Task 2: Footer and default title

**Files:** Modify `web/index.html` (footer, `#title` placeholder, static `#room-title` text), `web/app.js` (`DEFAULT_TITLE`), `web/style.css` (footer layout), `web/app_test.go`.

**Interfaces:** `DEFAULT_TITLE = 'Private conversation'`.

- [ ] **Step 1: Write the failing test** `TestFrontendFooterAndDefaultTitle`. It asserts:
  - **Footer.** `index.html` has no `/healthz` and contains `History lasts only until the room closes.`. The `<footer>` contains `End-to-end` zero times.
  - **Placeholder.** `placeholder="e.g. Weekend plans"`.
  - **Default title.** `app.js` has `DEFAULT_TITLE = 'Private conversation'`, and `Quiet conversation` appears nowhere in `index.html` or `app.js`. The `|| DEFAULT_TITLE` fallback stays, so a typed title is kept as is (Review Focus 2).
- [ ] **Step 2: Run to verify it fails.** Run `go test ./web/ -run TestFrontendFooterAndDefaultTitle`. Expected: FAIL.
- [ ] **Step 3: Implement.** Update any other tests that pinned the old copy.
- [ ] **Step 4: Run to verify it passes.** Run the full pre-commit line. Expected: PASS.
- [ ] **Step 5: Commit** `fix(web): honest footer; default title no longer looks like the placeholder`

---

### Task 3: Browser and iOS verification, docs, ship

- [ ] **Step 1: Rebuild and check what is served.** Run `docker compose up --build -d`, then confirm the served files match `web/`.
- [ ] **Step 2: Walk home and the guest screen in new tabs** at 1280, 375 and 320. Measure:
  - the minimum computed font-size on each screen (at least 12px);
  - heading tracking;
  - the position of the hero and the create button (Review Focus 3);
  - the footer (Review Focus 4);
  - an empty-title room showing "Private conversation" (Review Focus 2).
- [ ] **Step 3: Check the guest screen and home in the iOS Simulator.** The guest heading's letters must not touch.
- [ ] **Step 4: Run the detector and the full suite.**
  - Run `impeccable detect --json web/index.html`. Expected: none of the four rules in Review Focus 5. Record any other findings.
  - Run the full suite.
- [ ] **Step 5: Update docs and ship.** Update `docs/UI_TEST_CASES.md` (cases HOME-01…) and commit. The controller then pushes and opens the MR with auto-merge, and waits for the deploy.
