# Interface polish: type, sidebar and sheet, home and guest

Date: 2026-11-22 · Status: approved · Branches: `feat/polish-type`, then `feat/polish-sheet`, then `feat/polish-home`. Each is one MR to `main`, merged and deployed before the next one starts.

## Intent

After the trust strip, the guest screen and the mobile shell shipped, a walk on desktop, on mobile and in the iOS Simulator left a list of rough edges. The user asked for all of them to be fixed, in three MRs.

The biggest finding is that the product has no fixed face. `style.css` asks for `Inter`, but no font file ships, so every device falls back to its own system font: SF on iOS, Segoe on Windows, Arial elsewhere. The user chose to self-host one face for everyone, **Instrument Sans**, after a side-by-side comparison with Hanken Grotesk and Schibsted Grotesk.

## Goals

- One self-hosted text face (Instrument Sans) and one self-hosted monospace (JetBrains Mono) on every device.
- No interface element that looks broken, misleading or like filler. That covers the items listed under each MR below.
- Every visible text on every screen is at least 12px, and the type tracking is never tighter than -0.04em.

## Non-goals

- Server or protocol changes.
- New features. Delivery receipts beyond the existing "Sending…" → time change and read status are out of scope.
- A redesign of layout or colour. The palette and structure stay.
- The Android keyboard check. There is no emulator on this machine, so it stays a recorded gap.

## Anti-goals

- Copy that claims more than the product does.
- Monospace used as a "technical" costume. It is kept only for data: the timer, the safety code, the invite link and the cipher names.
- Fonts or any asset loaded from a third-party origin. The CSP stays `default-src 'self'`.

## Constraints

- Vanilla HTML/CSS/JS, no `innerHTML` (CSP with Trusted Types). Embedded assets must be listed in `web/embed.go` and covered by `web/manifest_test.go`.
- Existing Go string-contract tests in `web/app_test.go` pin some copy and markup. Update them deliberately, never delete them silently.
- Desktop (above 700px) and mobile (700px and below) layouts from the earlier MRs stay as they are, apart from what this document changes.

## Approaches

- **Order (decided by the user).** Three MRs: type and global first, then the sidebar and sheet, then home and guest. Type goes first because the face changes widths and line breaks everywhere, so later polish is tuned on the final font.
- **Face (decided by the user).** Self-host one face (A) rather than use honest `system-ui` (B). Of the three candidates, Instrument Sans (the user's pick) beat Hanken Grotesk (too generic) and Schibsted Grotesk (wider and heavier; longer phrases wrap).

## Design

### MR 1: `feat/polish-type`, type and global

1. **Fonts.**
   - Add `web/fonts/instrument-sans-latin.woff2`, `instrument-sans-latin-ext.woff2`, `jetbrains-mono-latin.woff2` and `jetbrains-mono-latin-ext.woff2` (variable fonts from Google Fonts, fetched once and committed).
   - Add `@font-face` rules with `font-display:swap` and the unicode-range for each subset.
   - The body stack becomes `"Instrument Sans", system-ui, sans-serif`. Every `monospace` stack becomes `"JetBrains Mono", ui-monospace, monospace`.
   - Embed them with a `fonts/*` pattern in `web/embed.go` and list them in the manifest test.
   - Commit each family's SIL Open Font Licence (`web/fonts/OFL-InstrumentSans.txt`, `OFL-JetBrainsMono.txt`) next to the files, embedded with them. *(Builder-check call.)* The woff2 files are already fetched from Google Fonts into a scratch folder. The licence texts come from the families' official repositories.
   - Add `<link rel="preload" as="font" type="font/woff2" crossorigin>` for the latin subset of Instrument Sans.
2. **Re-tune after the switch.** Check headings and buttons at 1280, 768, 375 and 320px. Fix any overflow or bad wraps the new metrics cause.
3. **Browser surfaces.**
   - `::selection` uses an accent tint with readable text.
   - `caret-color: var(--accent)` on inputs and textareas.
   - The existing scrollbar colour stays.
4. **The `↗` glyph goes everywhere.** It currently marks a submit button, a copy action, end-chat, decoration and an external link, and iOS renders it as a blue emoji. Internal actions get plain labels: "Create room", "Copy link", "End the chat for both". The decorative arrows on the home flow and the card corner go. An external link, where any remains, gets a drawn SVG arrow.
5. **Sent marker.** The user's own accepted messages show a small drawn check next to the time, with an accessible name of "Sent". Pending messages keep "Sending…", failed ones keep "Not sent".
6. **Disabled send button.** It becomes neutral (line colour, muted icon) instead of lime at 45% opacity, so it never reads as active.
7. **Opening another link in the same tab.** On `hashchange`, reload only if the new fragment has an `invite` or `room` parameter that differs from what this tab is showing. In every other case do nothing: the same room or invite, an empty fragment (e.g. Back after the app replaced it), or an unrelated fragment. That way a live session or a guest's only copy of an invite is never dropped. *(Builder-check call.)*

### MR 2: `feat/polish-sheet`, sidebar and sheet (desktop sidebar and mobile sheet are the same DOM)

1. **Notifications.**
   - When they are blocked or unsupported, show one muted line: "Notifications are blocked in this browser." No disabled bordered button.
   - The button exists only when notifications can be turned on or off.
2. **The reload note** ("You can reload the tab: while the room is open…") is removed.
3. **Participants heading.**
   - The `PARTICIPANTS` eyebrow becomes a plain section heading, "Participants", in the text face.
   - The `1 / 2` counter is replaced by a status line: "Waiting for your contact" while alone, nothing once both are present (the two names say it).
4. **Message counter.** "N / 500 messages" is shown only from 450 messages, with the same treatment as the byte counter.
5. **End the chat for both** becomes a full-width danger button (`.danger` style from the end dialog) at the bottom of the sidebar or sheet, with space above it.
6. **The `YOUR SPACE` eyebrow** is removed on desktop too. It is already hidden in the sheet.
7. **The 12px floor on desktop chat and sidebar.** The mobile floor already holds. Above 700px, the following still render below 12px and are raised to at least 12px in this MR: the composer hint, message meta, the conversation label, the timer label, avatars and the counters. *(Builder-check call.)*

Placement and semantics:
- End sits at the end of the scrolling content, not pinned. *(Builder-check call.)*
- The "Waiting for your contact" status line is a polite live region (`role="status"`), so the change is announced when the contact joins. *(Builder-check call.)*

### MR 3: `feat/polish-home`, home and guest screen

1. **Tracking.** The guest heading "You're invited to a private conversation" and every heading on home get letter-spacing no tighter than -0.04em at their size. Today the guest heading's letters touch on iOS.
2. **Labels and small text on home.**
   - Remove the eyebrow labels "A PRIVATE SPACE, FOR A WHILE" and "NEW CONVERSATION", and the `/ 01 / 02 / 03` numbers on the principles.
   - The 01/02/03 flow keeps its numbers, because they are a sequence.
   - Raise every remaining text to at least 12px: room facts, principle text, the flow and the footer.
3. **Footer.** Remove the "Server status" link to raw `/healthz` JSON, and the third repetition of "End-to-end encrypted". The footer keeps "nullchat © 2026" and one sentence on history: "History is available to participants until the room closes." (corrected in review: the earlier wording implied deletion, which SPEC.md does not promise)
4. **Default title.** An empty title no longer silently becomes the placeholder.
   - The input placeholder becomes "e.g. Weekend plans".
   - The default title shown in the room becomes "Private conversation". *(Builder call: the user asked to fix the confusion, not for specific copy.)*
   - It is renamed everywhere `DEFAULT_TITLE`, the static `<h2>` and the tests use it. Already-open untitled rooms then show the new default too, which is intended. *(Builder-check call.)*

## Verification (each MR)

- Go contract tests and node tests updated for every changed string or markup; `go test -race ./...` and `node --test web/*.test.mjs` green.
- Rebuild Docker, then walk the changed screens in the built-in browser at 1280 and 375px. Measure font sizes (all ≥ 12px) and confirm the fonts are loaded (`document.fonts`).
- Check the changed screens in the iOS Simulator (iPhone 17).
- Run the impeccable detector on `web/index.html`. MR 3 should leave no `tiny-text`, `undersized-ui-text`, `hero-eyebrow-chip` or `kicker-above-heading` findings, and no `overused-font` after MR 1.
- After each MR: push, auto-merge on green CI, then wait for the Render deploy (live `app.js` hash equals `main`) before starting the next.

## Left to the builder

- Exact sizes and weights within the new face, within the tokens.
- The check icon's drawing.
- Test names, and UI test case ids in `docs/UI_TEST_CASES.md`.
