# Polish 1/3: Type and Global Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship Instrument Sans and JetBrains Mono as self-hosted fonts, and fix the global rough edges:
- the `↗` glyph everywhere;
- selection and caret colours;
- a send button that looks active when it is disabled;
- no "Sent" marker on accepted messages;
- a new link opened in the same tab is ignored.

**Architecture:** Font files live in `web/fonts/` and are embedded with a `fonts/*` pattern. CSS gets `@font-face` rules and a `--mono` custom property. A new pure module, `web/nav.js`, decides whether a `hashchange` needs a reload, and is unit-tested like `trust.js`.

**Tech Stack:** Vanilla HTML/CSS/JS, Go `embed`, Go string-contract tests, `node --test`.

**Spec:** `docs/superpowers/specs/2026-11-22-polish-design.md` (section "MR 1")

## Global Constraints

- No third-party origins; CSP stays `default-src 'self'`. No `innerHTML`.
- Font files (already fetched): `/tmp/nullchat-fonts/fonts/instrument-sans-latin.woff2`, `instrument-sans-latin-ext.woff2`, `jetbrains-mono-latin.woff2`, `jetbrains-mono-latin-ext.woff2`. All four are variable fonts.
- Font stacks:
  - body: `"Instrument Sans", system-ui, sans-serif`;
  - mono: `--mono: "JetBrains Mono", ui-monospace, monospace`.
- Copy: "Create room", "Copy link", "Link copied", "End the chat for both", "Sent" (the accessible name of the check).
- Text at least 12px wherever this MR touches it. Tracking never tighter than -0.04em.
- Branch `feat/polish-type`; one MR to `main`.

## Review Focus

1. **A user who presses Back after the app replaced `#invite=` with `#room=`.** Nothing reloads and the session stays. Pinned by the `nav.js` tests in Task 3.
2. **A slow font load.** Text is visible immediately in the fallback (`font-display:swap`), with no invisible-text flash. Pinned by a CSS assertion in Task 1.
3. **A message going pending → accepted.** The check appears without re-announcing the whole message, so the node is still patched by key. Pinned in Task 3.
4. **The copy feedback "Link copied" after the arrow removal.** It still resets to "Copy link". Pinned in Task 2.
5. **The disabled send button.** It must still meet 3:1 contrast against the composer so it is visible, while clearly reading as inactive. Measured in Task 4.

---

### Task 1: Self-hosted fonts

**Files:**
- Create: `web/fonts/` with the four woff2 files copied from `/tmp/nullchat-fonts/fonts/`, plus `web/fonts/OFL-InstrumentSans.txt` and `web/fonts/OFL-JetBrainsMono.txt`. Take the licence texts from the families' official repositories: `https://raw.githubusercontent.com/google/fonts/main/ofl/instrumentsans/OFL.txt` and `https://raw.githubusercontent.com/JetBrains/JetBrainsMono/master/OFL.txt`.
- Modify: `web/embed.go` (add `fonts/*` to `//go:embed`); `web/manifest_test.go` (add the four woff2 names); `web/style.css`; `web/index.html` (add a preload link).
- Test: `web/app_test.go`

**Interfaces:**
- Produces: the CSS custom property `--mono`. Every former `monospace` font rule in `style.css` now uses `var(--mono)`.

- [ ] **Step 1: Write the failing test** `TestFrontendShipsItsOwnFonts`. It asserts:
  - **style.css:**
    - it has `@font-face` blocks for `"Instrument Sans"` and `"JetBrains Mono"`, each with `font-display:swap` and a `url(/fonts/…woff2)`;
    - `:root` has `font-family:"Instrument Sans"` and `--mono:"JetBrains Mono"`;
    - no `font:… monospace` rule remains outside the `--mono` definition (regex `font:[^;]*\bmonospace` matches only inside `--mono`);
    - the string `Inter` no longer appears.
  - **index.html:** `<link rel="preload" href="/fonts/instrument-sans-latin.woff2" as="font" type="font/woff2" crossorigin>`.
  - **Manifest:** `Manifest()` lists `fonts/instrument-sans-latin.woff2` and `fonts/jetbrains-mono-latin.woff2`.
- [ ] **Step 2: Run to verify it fails.** Run `go test ./web/ -run TestFrontendShipsItsOwnFonts`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Add `@font-face` rules for the latin and latin-ext subsets, with their `unicode-range` values as printed by `impeccable font-fetch`.
  - Weight ranges: Instrument Sans `400 700`, JetBrains Mono `100 800`.
  - Copy the font files and the licence texts into `web/fonts/`.
- [ ] **Step 4: Run to verify it passes.** Run `go test ./web/ && node --test web/*.test.mjs && node --check web/app.js`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): self-host Instrument Sans and JetBrains Mono`

---

### Task 2: Arrows, selection, caret, disabled send

**Files:** Modify `web/index.html`, `web/app.js` (the copy button builder in `buildInvite`, around the `'Copy link ↗'` strings), `web/style.css`. Test: `web/app_test.go`.

**Interfaces:** none new.

- [ ] **Step 1: Write the failing test** `TestFrontendNoArrowGlyphs`. It asserts:
  - **The `↗` glyph is gone.** Neither `index.html` nor `app.js` contains `↗`.
  - **Exact copy.** `index.html` contains `>Create room</button>` and `>End the chat for both</button>`. `app.js` contains `'Copy link'` and `'Link copied'`, and the copy-feedback reset still sets `'Copy link'` (Review Focus 4).
  - **Selection and caret.** `style.css` has a `::selection` rule using `--accent`, and `caret-color:var(--accent)`.
  - **Disabled send button.** In `style.css`, `.send-button:disabled` sets a `background` that is not `var(--accent)`, and no `opacity:.45` remains on it.
- [ ] **Step 2: Run to verify it fails.** Run `go test ./web/ -run TestFrontendNoArrowGlyphs`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Remove the `.flow-arrow` spans, the `.card-symbol` span and the `<span>↗</span>` in the create button, together with their CSS.
  - The footer link text becomes "Server status" without the glyph. MR 3 removes that link entirely.
  - The disabled send button gets the line-colour background and a muted icon colour.
- [ ] **Step 4: Run to verify it passes.** Run the full pre-commit line. Expected: PASS.
- [ ] **Step 5: Commit** `fix(web): drop the arrow glyph, theme selection and caret, neutral disabled send`

---

### Task 3: Sent check and same-tab links

**Files:**
- Create: `web/nav.js`, `web/nav.test.mjs`.
- Modify: `web/embed.go`, `web/manifest_test.go`, both CI files (add `web/nav.test.mjs`), `web/app.js` (`buildMessage`, plus a `hashchange` listener), `web/style.css`.
- Test: `web/app_test.go`

**Interfaces:**
- Produces: `needsReload(current: string, next: string) -> boolean` in `web/nav.js`. Both arguments are `location.hash` strings. It returns true only when `next` has an `invite` or `room` parameter and that value differs from the same parameter in `current`. An empty `next`, an unrelated fragment, or the same value all return false.

- [ ] **Step 1: Write the failing tests.**
  - `web/nav.test.mjs`:
    - `same room is no reload`: `('#room=a', '#room=a')` is false.
    - `empty fragment is no reload`: `('#room=a', '')` and `('#room=a', '#')` are false (Review Focus 1).
    - `unrelated fragment is no reload`: `('#room=a', '#top')` is false.
    - `different room reloads`: `('#room=a', '#room=b')` is true.
    - `invite after room reloads`: `('#room=a', '#invite=x&k=y')` is true.
    - `same invite is no reload`: `('#invite=x&k=y', '#invite=x&k=y')` is false.
  - Go test `TestFrontendSentCheckAndSameTabLinks`:
    - `app.js` contains `from './nav.js'` and `addEventListener('hashchange'`, the listener calls `needsReload(` and `location.reload()`;
    - in `buildMessage`, an accepted self message appends an element with `aria-label` `'Sent'` built via `createElementNS('http://www.w3.org/2000/svg'`;
    - `article.dataset.key` is still `${message.sender_id}:${message.client_message_id}` (Review Focus 3).
- [ ] **Step 2: Run to verify they fail.** Run `node --test web/nav.test.mjs; go test ./web/ -run TestFrontendSentCheckAndSameTabLinks`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - `nav.js` parses fragments with `URLSearchParams`.
  - The listener keeps the previous hash in a closure variable, updated on every `hashchange`.
  - The check is a 12px stroke SVG coloured `currentColor`, placed after the time in `.message-meta` for `self && status === 'accepted'`.
- [ ] **Step 4: Run to verify they pass.** Run the full pre-commit line. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): sent check on accepted messages; reload when a new link opens in the same tab`

---

### Task 4: Browser and iOS verification, docs, ship

**Files:** Modify `docs/UI_TEST_CASES.md` (cases TYPE-01…).

- [ ] **Step 1: Rebuild and check what is served.** Run `docker compose up --build -d`. Confirm the served `/app.js` and `/style.css` shasums match `web/`. Confirm `/fonts/instrument-sans-latin.woff2` returns 200 with type `font/woff2`.
- [ ] **Step 2: Walk the screens in new tabs** (built-in browser, 1280 and 375): home, guest screen, chat with two participants, sheet.
  - Check `document.fonts` loaded `Instrument Sans` and `JetBrains Mono`.
  - Check there is no overflow or broken wrapping in headings and buttons; fix any that appear.
  - Check the check mark appears on your accepted messages.
  - Check the disabled send button meets ≥3:1 against the composer background (Review Focus 5).
  - Check that opening a second invite link in the same tab loads it, and that Back does nothing harmful.
- [ ] **Step 3: Check the same screens in the iOS Simulator.**
- [ ] **Step 4: Run the detector and the full suite.**
  - Detector: `impeccable detect --json web/index.html`. There must be no `overused-font` finding.
  - Run `go test -race ./... && node --check web/app.js && node --test web/*.test.mjs`.
- [ ] **Step 5: Commit the docs.** The controller then pushes and opens the MR with auto-merge, and waits until live `/app.js` equals `main`.
