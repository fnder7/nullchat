# Trust strip: invite and safety-code verification in the conversation pane

Date: 2026-11-22 · Status: approved · Branch: `feat/trust-strip`

## Intent

The design critique of 2026-11-22 (`.impeccable/critique/2026-11-22T13-43-42Z__web-index-html.md`, score 20/40) found that the two moments that make nullchat different from any other chat are hidden in the sidebar:

- **P0:** the safety code is a 15px line in a sidebar card (below the chat on mobile). It has no action and no outcome. A user who compared codes and a user who ignored them see the same UI.
- **P1:** while the creator waits, the only job is to send the invite link. The link is the fourth sidebar block, a truncated read-only input, while the main pane shows an empty `[ … ]`.

The fix moves both into one always-visible strip at the top of the conversation pane. The strip walks the room through its trust lifecycle: send the link → check the person → done.

What the safety code really protects against (SPEC.md:151, docs/SECURITY.md): the signatures already catch a key swapped without the link secret (`keyFailure()`). The code's practical value is confirming that **the person on the other end is the one you meant to invite**, not someone who intercepted a link without a password. The copy is therefore phrased as "Is this really them?", not as cryptography.

## Goals

- The invite link is the most prominent thing on screen while the creator is alone.
- When the second key arrives, both participants see a large safety code and a clear question with two answers.
- The answer is visible afterwards as a badge in the conversation header, and it survives a page reload.
- "Doesn't match" leads straight to ending the chat, with a clear explanation.
- A key-verification failure (`keyFailure()`) is shown in the strip as an alarm, not just in a sidebar card.

## Non-goals

- Blocking or gating the composer until codes are compared. The user chose a nudge (option A): writing is possible as soon as the key exists.
- Sharing the verification status with the other participant. The status is **local to each browser**; no protocol or backend change.
- The guest invite screen, the used-invite state and any pre-join preview endpoint. That is the next project.
- The mobile app shell (sticky header, pinned composer, 12px text floor). That is the project after this one. The strip must work in today's mobile layout, but the layout itself is not reworked here.
- Ended/expired screen rework (P2), except that the strip hides on `finish()`.

## Anti-goals (would be a failure even if it works)

- The strip steals focus or interrupts typing when its state changes.
- The code or the buttons sit inside the `role="log"` live region, so a screen reader rereads them with messages.
- The strip permanently eats a large part of a small screen after the user has answered.
- Copy that implies the server or nullchat verified anything. Only the people can.

## Constraints

- Vanilla JS/HTML/CSS, no dependencies (README). CSP with Trusted Types (`trusted-types 'none'`): DOM is built with `createElement`/`textContent`, no `innerHTML`.
- `#messages` is `role="log"` and is patched by key in `renderMessages()`; the strip must live outside it.
- Session state is persisted by `save()` into `sessionStorage` under `nullchat:<room id>` and wiped in `finish()`.
- `state.peers` pins both public keys; `establishKey()` sets the code once both keys are present.
- Existing contract tests in `web/app_test.go` read the static files as strings; the new behaviour gets tests in the same style.
- Visual language stays: dark green ground, lime accent, monospace for the timer and code (style.css tokens).

## Approaches

1. **Card inside the message stream.** Reads like a conversation event, but it lives in the `role="log"` region that `renderMessages()` patches; buttons in a log confuse screen readers, and the card scrolls away after a few messages (the same "hidden" problem on mobile).
2. **Pinned strip above the message list. (Chosen.)** A single component between `.conversation-head` and `.messages-wrap`, independent of message rendering. It is always visible, handles the whole lifecycle, and works on mobile. It replaces the sidebar `share-panel` and most of `safety-panel`.
3. **Invite in the empty state, verification behind a header badge.** Less new code, but the invite goes back into the live log and verification hides behind a click, which repeats today's problem.

The user picked approach 2.

## Design

### Placement

`<section id="trust-strip" class="trust-strip" aria-labelledby="trust-title">` inside `.conversation`, after `.conversation-head`, before `.messages-wrap`. A badge `<button id="trust-badge">` is added to `.conversation-head`, next to the connection state.

### States

The strip derives its state from existing state plus one new field, `state.e2ee.verified`:

| State | When | Strip content | Badge |
|---|---|---|---|
| `invite` | creator, alone, invite link available | Heading "Send this link to your contact". The full link (wrapping, monospace, selectable; not a truncated input). Primary **Copy link**; **Share…** when `navigator.share` exists. Warning: "Anyone who opens this link takes the seat. Send it somewhere private." If the room has a password: "Send the password separately." | hidden |
| `waiting` | alone, no invite available (the invite was revoked) | Alarm-coloured: "The invite was revoked after 5 wrong password attempts. End the chat and create a new room." Moves the current `chat-error` text from the `invite_revoked` handler. | hidden |
| `verify` | both keys present, `verified` unset | Heading "Is this really them?". The safety code large (monospace, ≥ 24px, grouped as today). Text: "Compare this code with your contact by voice or in another app. If it matches, you are talking to the right person." Buttons **It matches** (primary), **It doesn't match** (danger), **Later** (text link). | "Not verified" (neutral/amber) |
| `verified` | `verified === 'match'` | Collapsed: hidden. When expanded via the badge: the code, "You confirmed this code matches.", a **Compare again** link (resets `verified` to unset, back to `verify`) and a **Hide** link. | "✓ Verified" (lime) |
| `later` | `verified === 'later'` | Collapsed: hidden. Expanding via the badge shows the `verify` content again. | "Not verified" |
| `broken` | `state.keyError` | Alarm: "Key verification failed" plus today's `keyFailure()` note, and an **End the chat** button. Not collapsible. | "Key error" (danger) |

Neither participant sees the other's choice. The guest never sees `invite` or `waiting`, because they join into a room where the creator is already present, so they go straight to `verify`.

### Behaviour

- **It matches** sets `verified = 'match'`, saves, and collapses the strip. The badge becomes "✓ Verified".
- **Later** sets `verified = 'later'`, saves, and collapses. The badge stays "Not verified".
- **It doesn't match** opens the end-chat dialog with the mismatch wording (below). Confirming ends the chat through the existing `/api/v1/end` call and `finish()`. Cancelling leaves `verified` unset, so the strip stays in `verify`.
- The **badge** is a button in every state where it is visible (`aria-expanded`, `aria-controls="trust-strip"`). In `verified` and `later` it toggles the strip open or closed. In `verify` the strip is open (`aria-expanded="true"`) and clicking the badge collapses it, which is the same as **Later** (sets `verified = 'later'`). There is no unpersisted "collapsed but unanswered" state. In `broken` the badge is shown but the strip cannot collapse; clicking it moves focus to the strip heading. *(Builder-check call.)*
- **Compare again** (in expanded `verified`) clears `verified` and shows `verify`, so a misclick on **It matches** can be corrected. *(Builder-check call.)*
- **Copy link** keeps today's behaviour: the clipboard, with a select-the-text fallback and the label feedback. **Share…** calls `navigator.share({ url })` and ignores `AbortError`.
- **Persistence:** `verified` is stored in `state.e2ee` next to `peers`, so `save()` writes it and a reload restores it. If `peers` changes, `keyFailure()` already fires; `verified` is meaningless then and the `broken` state wins. `finish()` already removes the session entry.
- **On `finish()`** the strip and badge are hidden.
- **No focus moves** on any automatic state change, with one exception: if focus was inside the strip and the change removes the focused control (for example **Copy link** when the guest joins, or the verify buttons on `keyFailure()`), focus moves to the strip heading (`#trust-title`, `tabindex="-1"`) so it never drops to `<body>`. Focus that is elsewhere (the composer, the sidebar) is never touched. After the user collapses the strip, focus goes to the badge. *(Builder-check call.)*

### End-chat dialog (replaces `confirm()`)

A `<dialog id="end-dialog">` opened with `showModal()`. One opener function takes a reason (`'end' | 'mismatch' | 'broken'`) and the element to return focus to. It is used by the sidebar "End the chat for both", "It doesn't match" and the `broken` state's **End the chat**, with different text:

- From End: "End the conversation? The room and its history will be gone for both of you." Buttons: **End for both** (danger) and **Cancel**.
- From mismatch: "The codes don't match. The person on the other end may not be your contact. End the conversation for both?" Same buttons.
- From broken: "Key verification failed, so this conversation is not safe. End it for both?" Same buttons. Ending a room for both people always takes a confirmation, even here. *(Builder-check call.)*

Esc and Cancel close it without action. Focus returns to the control that opened it. The focus trap is native to `showModal()`.

### Sidebar changes

- Remove `#share-panel` (moved to the strip).
- Remove `#safety-panel`'s code and note. Keep a one-line "End-to-end encrypted in your browser" so the sidebar still states encryption. `#safety-alert` (`role="alert"`) moves into the strip, so the key-failure announcement stays.
- The rest of the sidebar is untouched in this project.

### Accessibility

- The strip is a labelled region outside the live log. State text changes are announced through one polite `role="status"` element inside the strip, with short text: "Your contact joined. Compare the safety code." / "Marked as verified." Key failure keeps the assertive `role="alert"`.
- The code is rendered as text with `aria-label` reading the digit groups, so a screen reader does not read it as one large number.
- All buttons are real `<button>`s; the badge has `aria-expanded`.

### Mobile (today's layout)

The strip sits at the top of `.conversation`, so it is visible as soon as the chat area is on screen, unlike today's sidebar block below the chat. In the collapsed states it takes no space. The full-height shell is not part of this project.

## Left to the builder

- Exact spacing, sizes and colours within the existing token set (the code at least 24px; any text in the strip at least 12px).
- The DOM-building helpers and whether the strip renders through one `renderTrust()` function called from `renderParticipants()`, `establishKey()`, `keyFailure()` and `finish()` (suggested).
- The amber shade for "Not verified", if the palette has none (derive from existing tokens; check contrast ≥ 4.5:1).
- Test wording in `web/app_test.go` and new cases in `docs/UI_TEST_CASES.md`.
