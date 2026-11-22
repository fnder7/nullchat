# Mobile chat shell

Date: 2026-11-22 · Status: approved · Branch: `feat/mobile-shell` (from `main` at 53bf998)

## Intent

The design critique of 2026-11-22 (`.impeccable/critique/2026-11-22T13-43-42Z__web-index-html.md`) found the mobile chat to be a scroll-in-scroll trap (P1).

- Below 700px the sidebar dissolves into the page grid (`style.css`, the `.chat-layout` / `.room-sidebar{display:contents}` block in the second `@media(max-width:700px)`). `.conversation` gets `height:calc(100dvh - 245px)`, and the page scrolls around it.
- So the room title and the timer scroll away above, while encryption, notifications and **End the chat for both** scroll away below.
- Text at that width drops to 9–11px: eyebrows, timer label, avatars, the connection line and the composer hint.

The user wants the phone chat to behave like an app. The room title and timer stay visible, the composer sits at the bottom, and only the message list scrolls. Secondary controls, including End, live behind a ⋯ button. The user decided both: End goes behind ⋯ (option chosen in conversation), and the build uses a compact head plus a sheet (approach 1).

## Goals

- At widths ≤ 700px the chat screen is one viewport tall, and the page itself does not scroll. Three parts are visible at all times:
  - **Head:** room title, a timer chip and ⋯.
  - **Status row:** connection state and the trust badge, i.e. the existing `.conversation-head`.
  - **Body:** the trust strip when it is open, then the message list (the only scroller), then the composer and its hint.
- The composer stays visible above the on-screen keyboard on iOS Safari and Android Chrome.
- ⋯ opens a bottom sheet with participants, the encryption note, notifications, the reload note and **End the chat for both**.
- Every piece of text on the mobile chat screen is at least 12px.
- The desktop and tablet layouts (> 700px) are unchanged.

## Non-goals

- The home page, the guest screen, and their small text. These go to the polish pass.
- Changing what is in the sidebar, or any copy in it.
- Gestures (swipe to close the sheet), animation beyond a short slide, and landscape-specific layouts beyond "it still works".

## Anti-goals

- Two scrollable regions on the mobile chat at the same time (the page and the list).
- Opening the sheet steals focus while the user is typing. It opens only on a tap of ⋯.
- A live region (the timer's one-minute warning, `#safety-alert`) is lost because its container is `display:none` on mobile.

## Constraints

- Vanilla JS/HTML/CSS, no `innerHTML` (CSP with Trusted Types). Existing tokens only. Text at least 12px, contrast at least 4.5:1.
- The DOM stays single-sourced for the sidebar: the sidebar *is* the sheet on mobile. Only the room title and the timer value are mirrored into the head.
- `enter()` focuses `#room-title` (a contract test pins this). On mobile that element is inside the closed sheet.
- `updateTimer()` writes `#timer`, `#timer-bar` and `.ending` on `#timer-card`, plus `#timer-alert` (role=status). `finish()` removes `.ending`.
- The trust strip and the badge (from MR !9) stay where they are, in `.conversation`.

## Approaches

1. **Compact head plus the sidebar as a sheet (chosen by the user).** A new `.chat-head`, visible only on mobile, mirrors the title and timer. The ⋯ button opens the existing `.room-sidebar` as a fixed bottom sheet. There is one DOM and desktop is untouched. The cost is two mirrored text values.
2. **Move nodes with JS on breakpoint changes (`matchMedia`).** No duplication, but it breaks easily on rotation and risks losing focus and live-region state.
3. **The sheet as a `<dialog>` that receives the sidebar's children on open.** The focus trap comes for free, but nodes still have to be moved in and out each time, so it has the same risks as 2.

## Design

### Markup

- `<header class="chat-head">` is the first child of `<main id="chat">`. It is hidden above 700px by CSS. It contains:
  - `<h2 id="chat-head-title" tabindex="-1">`, which mirrors `#room-title`;
  - `<span id="chat-head-timer" class="timer-chip" aria-hidden="true">`, which mirrors `#timer`. It is aria-hidden because the sidebar timer stays the one announced source;
  - `<button id="sheet-button" type="button" aria-controls="room-sheet" aria-expanded="false" aria-label="Room details">⋯</button>`.
- `.room-sidebar` gets `id="room-sheet"`. On mobile it also gets the attributes `role="dialog"`, `aria-modal="true"` and `aria-label="Room details"`. Set them from JS only while the sheet is open, so desktop semantics stay as today.
- The sheet gets a close button at its top: `<button id="sheet-close" type="button" aria-label="Close">×</button>`. It is mobile-only, via CSS.

### Layout at ≤ 700px

- On the chat screen, hide the site `.header` and `footer`. Use a body class `in-chat`, set in `enter()` and kept after `finish()`.
- `#chat` becomes a flex column with `height: var(--app-height, 100dvh)` and `overflow: hidden`.
  - `.chat-head` and `.conversation-head` have a fixed height.
  - `.conversation` is a flex column with `flex: 1; min-height: 0`.
  - `.messages-wrap` gets `flex: 1; min-height: 0` and is the only scroller.
  - `.composer` and `.composer-hint` sit at the bottom.
  - The old `height:calc(100dvh - 245px)` and `min-height:430px` are removed at this width.
- The sheet (`.room-sidebar`) is `position: fixed; inset: auto 0 0 0; max-height: 85%; overflow-y: auto`. It uses the panel background, a top border and a top radius, and is hidden until open. A backdrop element `<div id="sheet-backdrop">` covers the rest.
  - The closed sheet is `display:none`, so it is out of the tab order and the accessibility tree.
  - `#timer-alert` (role=status) moves out of `.timer-card` to a spot inside `#chat` that is never hidden, for example right after `.chat-head`. It is moved, not mirrored, so the one-minute warning is still announced while the sheet is closed. *(Builder-check call.)*
  - Inside the open sheet, `#room-title` is shown in full, so a sighted user can read a title the head truncates. The sheet starts with this title, followed by participants, encryption, notifications, the reload note and End. `.timer-card` is hidden in the sheet, because the head chip shows the time. *(Builder-check call.)*
  - `#chat` on mobile is `position:fixed; left:0; right:0; top:0`, with `height: var(--app-height, 100dvh)`. This locks the page, so nothing outside the list scrolls. *(Builder-check call.)*
- Text floor: every rule inside the mobile chat that is below 12px is raised to 12px. That covers `.conversation-label` (hidden anyway), the eyebrows, `.timer-card>span`, `.avatar`, the connection line, `.composer-hint`, `.sidebar-note` and the participants count.

### Keyboard

- `<meta name="viewport">` adds `interactive-widget=resizes-content`. Android Chrome then shrinks the layout viewport when the keyboard opens.
- iOS Safari does not resize the layout viewport. While `#chat` is visible, `visualViewport` `resize` and `scroll` listeners set two properties on `#chat`: `--app-height` to `visualViewport.height` px, and `top` to `visualViewport.offsetTop` px. Reset both when the chat is hidden. *(Builder-check call: the offset is handled because iOS scrolls the visual viewport when the textarea is focused.)*
- **Trust strip versus the keyboard.** While the composer has focus at ≤ 700px, an open trust strip in `verify`, `verified` or `later` is hidden. The badge stays, and the strip comes back on blur. `state.e2ee.verified` and `state.trustOpen` do not change. In `invite` and `broken` the composer is disabled, so the keyboard never opens. This keeps one scroller and a visible composer with the keyboard up. *(Builder-check call.)*
- When the composer gains focus, the message list keeps its bottom anchor: if the user was at the bottom, they stay there after the resize.

### Sheet behaviour

- ⋯ opens the sheet:
  - set `aria-expanded="true"`;
  - show the sheet and the backdrop;
  - set the dialog attributes;
  - move focus to `#sheet-close`;
  - make `.conversation` and `.chat-head` `inert` while the sheet is open.
- Close (`#sheet-close`, a backdrop tap, Esc) reverses all of that and returns focus to ⋯.
- Opening `#end-dialog` from inside the sheet works as today. When the dialog closes with Cancel, focus returns to `#end-button` inside the still-open sheet.
- `finish()` closes the sheet if it is open. After the end, on mobile:
  - the head keeps the title;
  - the timer chip reads "Ended" without the `.ending` colour;
  - ⋯ is hidden, because what is left in the sheet no longer matters;
  - the ended panel fills the body. *(Builder-check call.)*
- The `in-chat` body class follows `#chat` visibility. It is set in `enter()` and removed wherever `#chat` is hidden again, for example `restoreFailed()`, so the home screen never loses its header. *(Builder-check call.)*
- The trust strip's mismatch and broken paths open `#end-dialog` from the conversation with the sheet closed. This works as today.
- If the viewport grows past 700px while the sheet is open (rotation, resize), it closes without moving focus. Use a `matchMedia('(max-width:700px)')` change listener.
- The slide-in uses `transform` over at most 200ms, and none under `prefers-reduced-motion`.

### Mirroring

- `updateTimer()` also writes the same string to `#chat-head-timer`, and toggles `.ending` on it next to `#timer-card`. `finish()` removes `.ending` from both.
- Wherever `#room-title` text is set, `#chat-head-title` gets the same text.
- `enter()` focuses `#chat-head-title` when the mobile query matches, and `#room-title` otherwise. A minimal edit to the existing focus assertion in `web/app_test.go` is acceptable if a helper is cleaner, as long as both targets stay pinned. *(Builder-check call.)*

### Verification

- **Static.** Go string-contract tests, as in the rest of `web/app_test.go`.
- **Built-in browser.** A walk at 375×812 and at 320px: one scroller, the sheet's open and close paths with focus, End from the sheet, the strip with the composer focused, the end state, and text ≥ 12px measured with `getComputedStyle`.
- **Keyboard.** An iOS Simulator check of the keyboard behaviour if Xcode is available on this machine. If not, the report says so. *(Builder-check call.)*

## Left to the builder

- Exact sizes of the head, the chip and the sheet within the tokens. Suggested: head height around 56px, the title truncated with an ellipsis, the chip in monospace at 14–16px.
- Whether the backdrop is a real element or `::backdrop`-like styling on a sibling.
- How `inert` is toggled, as long as it is removed on every close path.
- Test names and the `docs/UI_TEST_CASES.md` case ids.
