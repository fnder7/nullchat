# UI test cases

The standing set of manual UI/E2E checks for nullchat. It verifies the user-facing
contract of the whole application, including the behavior of PostgreSQL, the HTTP
API and WebSocket as observed through the interface.

## Run rules

- After every change, run the P0 smoke (`UI-01`–`UI-09`).
- After changes in an affected area, also run the related P1 cases.
- Before a release, run the full P0 and P1.
- For the two participants, use independent browser contexts so they do not share
  `sessionStorage`.
- Record each case's result as `PASS`, `FAIL` or `BLOCKED`. For `FAIL`, give the
  actual result and the reproduction step; for `BLOCKED`, the missing dependency.
- During the run, check the visible result, the browser console and any unexpected
  HTTP/WebSocket errors.

## P0 — smoke after every change

| ID | Preconditions and steps | Expected result |
|---|---|---|
| UI-01 | Open the home page. | The page and static assets load, the layout is not broken, no console errors. |
| UI-02 | Create a room with a title and TTL. | The chat opens; the title, timer, creator and a successful connection status are visible. |
| UI-03 | Copy the invite, open it in an independent browser context and join. | The second user joins the same room; both see two participants. |
| UI-04 | Send one message from each participant. | Both messages appear for both participants in the same order, without duplicates. |
| UI-05 | Reload both tabs. | Sessions, participants and confirmed history are restored. |
| UI-06 | Temporarily break the WebSocket connection and restore it. | The UI shows reconnection, blocks sending during the outage and restores history after reconnecting. |
| UI-07 | Restart the backend without restarting PostgreSQL, before the TTL ends. | Both tabs reconnect; the room, sessions, participants and confirmed messages are preserved. |
| UI-08 | End the chat from one of the participants. | Both participants see the end; sending and rejoining are impossible; the visible history is cleared. |
| UI-09 | Check the console and network requests over the whole scenario. | No unhandled JS errors and no unexpected HTTP/WebSocket errors. |

## P1 — main regression

### Room creation

| ID | Preconditions and steps | Expected result |
|---|---|---|
| ROOM-01 | Create a room without a title. | A correct default title is used. |
| ROOM-02 | Create a room with a 60-character title. | The title is accepted, saved and fully available in the interface. |
| ROOM-03 | Check every available TTL value in turn. | For each value, a room is created with the matching lifetime. |
| ROOM-04 | Trigger a network retry of a single create request. | Two rooms are not created; the retry returns the original room and session. |
| ROOM-05 | Try to create a room while the DB is unavailable. | A clear temporary error is shown; the UI does not enter a broken state. |

### Invite and participants

| ID | Preconditions and steps | Expected result |
|---|---|---|
| JOIN-01 | Open a valid link and join. | The guest is added; the counter shows `2 / 2` for both. |
| JOIN-02 | After the guest has joined, open the same invite in a third context. | Joining is refused; a controlled error is shown without revealing room data. |
| JOIN-03 | Trigger a network retry of a single join request. | The original session is returned; no extra participant is created. |
| JOIN-04 | Open a damaged or unknown invite link. | A controlled error is shown; the application keeps working. |
| JOIN-05 | Copy a `#room=...` URL into a new independent context. | Without a saved session, no access to the room or its history is granted. |
| JOIN-06 | Reload a participant's tab. | Their session, name and the "you" marker are preserved. |

### Guest screen

An invite link (`#invite=…`) opens `#invite`, a dedicated guest screen, instead of the home page. The server never says why a link cannot be opened, so the closed state covers every cause with one text.

| ID | Preconditions and steps | Expected result |
|---|---|---|
| GUEST-01 | Open a valid link without a password. | The heading "You're invited to a private conversation", the three facts, **Join the conversation** and **Or create your own room**. No creator hero, no principles strip, no password field. |
| GUEST-02 | Open a valid link with a password (`pw=1`). | The same screen plus the field "Room password" and its hint. |
| GUEST-03 | At 375px width, open GUEST-01 and GUEST-02 and compare the Join button's `getBoundingClientRect().bottom` with `innerHeight`. | Join is inside the first viewport without scrolling; no horizontal scroll. |
| GUEST-04 | On a link without a password, press **Join the conversation**. | The guest joins; the chat opens with `2 / 2`; the fragment becomes `#room=…`. |
| GUEST-05 | On a password link, submit an empty field. | "Enter the room password." under the field; `aria-invalid="true"`; `aria-describedby` includes the message; focus is in the field; no join request is sent. |
| GUEST-06 | Type a wrong password and press Enter. | Enter submits. "Wrong password, or the link is damaged. After 5 wrong attempts the link stops working." under the field; `aria-invalid="true"`; focus in the field; the screen stays in `ready`. |
| GUEST-07 | After GUEST-06, edit the field. | The error text is removed, `aria-invalid` is gone and `aria-describedby` points at the hint only. |
| GUEST-08 | Enter the correct password and press **Join the conversation**. | The guest joins and the chat opens. |
| GUEST-09 | Open an already-used link. | "This link can't be opened", the spec's two paragraphs, the link "Create your own room" to `/`, no Join button; focus on `#invite-title`. The same text is shown for a revoked, closed or unknown invite. |
| GUEST-10 | Open a link whose `k` is truncated. | "This link is incomplete" at once, focus on `#invite-title`, no Join button, the link "Create your own room" to `/`, no `/api/` request. |
| GUEST-11 | Open a link whose token is 63 characters. | The same as GUEST-10 (including the "Create your own room" link), with no `/api/` request. |
| GUEST-12 | Open a link on a non-secure origin (no `crypto.subtle`). | "Open this link over HTTPS", no button, no request. Not observed in the browser (`127.0.0.1` is a secure context): the state mapping and copy are asserted in invite.test.mjs and app_test.go; the UI behaviour is not exercised in a browser. |
| GUEST-13 | Cause `rate_limited`, `database_unavailable`, `capacity_reached` or a network failure on join. | The screen stays in `ready`; the message sits under the button; the button is enabled for a retry. Not observed in the browser: the state mapping and copy are asserted in invite.test.mjs and app_test.go; the UI behaviour is not exercised in a browser. |
| GUEST-14 | Open the home page without a fragment. | Unchanged: "A place for two" card, no invite panel, `#invite` hidden. |
| GUEST-15 | Check the console and the Network tab over GUEST-01…11. | No JS errors. The only failed request is the expected `410` for a used link. |
| GUEST-16 | Open a fresh link without a password whose `k` is changed to another valid 43-character base64url value, then press **Join the conversation**. | The server answers `join_proof_invalid`. There is no field, so "Wrong password, or the link is damaged. After 5 wrong attempts the link stops working." appears under the button in `#join-error` (`role="alert"`); the screen stays in `ready` and the button is enabled. Each try counts toward the 5 attempts. |
| GUEST-17 | At desktop width (1024x768), open a valid link with a password, one without, and a used link. | The card is centred (equal side margins), no horizontal scroll, Join is visible without scrolling; the used link shows the closed state with no Join button. |

### Trust strip and safety-code verification

The strip (`#trust-strip`) sits above the messages; the badge (`#trust-badge`) sits in the conversation header. Verification status is local to each browser and is never sent to the other participant.

| ID | Preconditions and steps | Expected result |
|---|---|---|
| TRUST-01 | As the creator, create a room (with and without a password) and stay alone. | The strip shows "Send this link to your contact", the full link as wrapping monospace text and the warning. With a password it also says "Send the password separately." There is no badge. |
| TRUST-02 | In the invite state, press **Copy link**. In a browser with `navigator.share`, also check **Share…**. | The link is copied (or selected as a fallback) and the label changes. **Share…** appears only where `navigator.share` exists. |
| TRUST-03 | Join as the guest while the creator's focus is on **Copy link**. | The creator's focus moves to the strip heading, not to `<body>`. If focus was in the composer, it stays there. A polite status says "Your contact joined. Compare the safety code." |
| TRUST-04 | Check both tabs after the join. | Both show "Is this really them?", the same large (at least 24px) monospace code, **It matches**, **It doesn't match**, **Later**, and the badge "Not verified". |
| TRUST-05 | Press **It matches**, then reload. | The strip collapses and takes no space; focus goes to the badge; the badge reads "✓ Verified" and still does after reload. The other participant's tab is unchanged. |
| TRUST-06 | Open the badge in the verified state, press **Hide**, open it again, then **Compare again**. | The expanded strip shows the code and "You confirmed this code matches." **Hide** collapses it. **Compare again** returns to "Is this really them?" and the badge to "Not verified". |
| TRUST-07 | Press **Later**, then reload. | The strip collapses, the badge stays "Not verified" and survives reload. Opening the badge shows the verify content again. |
| TRUST-08 | Press **It doesn't match**, then **Cancel**. | The dialog shows the mismatch wording; Cancel closes it, focus returns to **It doesn't match**, the strip stays in the verify state. |
| TRUST-09 | Press **It doesn't match**, then **End for both**. | The chat ends for both participants; the strip and badge are hidden. |
| TRUST-10 | Open the sidebar **End the chat for both** dialog in one tab and end the chat from the other tab. | The dialog closes by itself when the end arrives; no error is shown. |
| TRUST-11 | Press Esc in the end dialog. | The dialog closes without ending anything and focus returns to the opener. |
| TRUST-12 | Provoke a key-verification failure. | The strip shows the alarm "Key verification failed" with an **End the chat** button; the badge reads "Key error"; **End the chat** asks for confirmation with the broken wording. Not observed in the browser; covered by source tests. |
| TRUST-13 | Exhaust the password attempts (5 wrong) so the invite is revoked. | The creator's strip shows the revoked-invite message with no link and no badge. Not observed in the browser; covered by source tests. |
| TRUST-14 | At 375px width, check the invite, verify and collapsed states. | The strip is at the top of the conversation; there is no horizontal scroll; text is at least 12px; collapsed it takes no height. |
| TRUST-15 | Run the screen-reader and keyboard pass over the strip. | The strip is a labelled region outside the message log; the code is read in groups; the badge announces its expanded state; status changes are announced politely. |

### Messages

| ID | Preconditions and steps | Expected result |
|---|---|---|
| MSG-01 | Send plain text. | The message goes through the sending and confirmed states. |
| MSG-02 | Type text and press Enter. | The message is sent. |
| MSG-03 | Type two lines using Shift+Enter. | A new line is added; nothing is sent prematurely. |
| MSG-04 | Try to send empty or whitespace-only text. | The message does not appear in the history. |
| MSG-05 | Send a message at the allowed size limit. | The message is confirmed and shown to both. |
| MSG-06 | Try to exceed the allowed size. | Input is limited or the server returns a clear error; the UI stays stable. |
| MSG-07 | Send HTML and the string `<script>alert(1)</script>`. | The string is shown as text; the code does not run and the DOM is not injected. |
| MSG-08 | Retry an unconfirmed message after a network error. | The message is saved and shown at most once. |
| MSG-09 | Reload the page after a message is confirmed. | The message is restored from persistent storage. |
| MSG-10 | Quickly send several messages from both participants. | Message order is the same on both clients. |
| MSG-11 | Reach the room's message limit. | The `message_limit` state appears; previously confirmed history stays available. |

### Connection, DB and recovery

| ID | Preconditions and steps | Expected result |
|---|---|---|
| NET-01 | Switch the browser to Offline with the chat open. | A connection loss is shown; sending is blocked. |
| NET-02 | Restore the network. | Automatic reconnection happens and the current history loads. |
| NET-03 | Do not restore the network until the automatic attempts run out. | A clear error state and a manual retry button appear. |
| NET-04 | Restore the network and press the manual retry button. | The connection is restored; the chat is available again. |
| NET-05 | Send a message right before the outage. | After reconnect, the confirmed message is neither lost nor duplicated. |
| NET-06 | Restart the backend, keeping the DB and its volume. | Active sessions and confirmed history are restored. |
| NET-07 | Temporarily stop PostgreSQL. | The UI reports temporary unavailability and does not pretend a send succeeded. |
| NET-08 | Bring PostgreSQL back before the TTL ends. | Clients reconnect; the room's confirmed data is preserved. |

### Ending and TTL

| ID | Preconditions and steps | Expected result |
|---|---|---|
| END-01 | End the chat as the creator. | The room closes for both participants. |
| END-02 | End the chat as the guest. | The room closes for both participants. |
| END-03 | Reload the page after ending. | Access and history are not restored. |
| END-04 | Reopen the invite of an ended room. | Joining is refused. |
| END-05 | Create a one-minute room and wait for the TTL. | Both participants see the "The conversation time is over" state. |
| END-06 | Try to send a message after the TTL. | Sending is impossible. |
| END-07 | Restart the backend before the TTL ends. | The room stays active only until the original `expires_at`; the TTL does not restart. |

### Storage and security

| ID | Preconditions and steps | Expected result |
|---|---|---|
| SEC-01 | Inspect `sessionStorage` during an active chat. | Current session data is stored, but no message history text. |
| SEC-02 | End the room and inspect `sessionStorage`. | This room's session entry is removed. |
| SEC-03 | Request a room without a session token. | There is no access to the room or its history. |
| SEC-04 | Provoke an API error and check the UI/response. | No SQL, DSN, credentials, stack trace or internal details are revealed. |
| SEC-05 | Use HTML/JS in the title and messages. | User content is escaped; there is no XSS. |

### Privacy and enumeration protection

| ID | Preconditions and steps | Expected result |
|---|---|---|
| PRIV-01 | In two clean browser contexts, open a URL with an existing and a random `#room=<64 hex>` without a saved session. | The text, visible state and the return to the home page are identical; the room's existence is not confirmed. |
| PRIV-02 | Compare joining with a used, a closed and a random well-formed invite token. | All variants return the same public `room_unavailable` state without room details. |
| PRIV-03 | Open a malformed invite token and try to join. | A neutral invalid-invite message is shown, with no room details and no irrelevant text about messages. |
| PRIV-04 | Check Network when opening `#room` without a local session. | The frontend does not request the room by `room_id`; there is no HTTP/WebSocket request that could confirm its existence. |

### Interface and accessibility

| ID | Preconditions and steps | Expected result |
|---|---|---|
| VIEW-01 | Check the home page and chat in a desktop viewport. | No overlaps, no clipped required elements, no horizontal scroll. |
| VIEW-02 | Check the home page and chat in a `390x844` viewport. | The main actions are available; no horizontal overflow. |
| VIEW-03 | Go through the main scenario with the keyboard. | Interactive elements are reachable without a mouse, focus is visible, Enter/Shift+Enter work per the contract. |
| VIEW-04 | Watch the buttons during a request, a disconnect and an end. | Buttons and fields are correctly disabled and do not allow repeated conflicting actions. |
| VIEW-05 | Check long allowed titles and messages. | Content wraps and does not break the layout. |
| VIEW-06 | Watch the timer before and after refresh/reconnect. | The timer counts down and continues from the server's `expires_at` without resetting. |
| VIEW-07 | Compare user-facing texts with the current storage contract. | The UI does not claim that confirmed history is lost on a backend restart if it is stored in PostgreSQL. |
| VIEW-08 | Run a Lighthouse accessibility audit of the home page. | No failed accessibility audits; interactive elements have consistent visible and accessible names. |

### Type and global polish

| ID | Preconditions and steps | Expected result |
|---|---|---|
| TYPE-01 | Open the home page. Read `document.fonts` and `getComputedStyle(document.body).fontFamily`. | Instrument Sans and JetBrains Mono are `loaded`; the body stack starts with "Instrument Sans". `/fonts/instrument-sans-latin.woff2` returns 200 as `font/woff2` and the served `/app.js`, `/style.css`, `/nav.js` match `web/`. |
| TYPE-02 | Check headings and buttons on home, the guest screen, the chat and the sheet at 1280, 768, 375 and 320 wide. | No overflow (`scrollWidth` equals `innerWidth`, no clipped button or heading) and no broken wraps. Walked at 1280 and 375 (earlier) and at 768 and 320 (home, guest, chat with two participants, ended screen; the sheet at 320): `scrollWidth` equals `innerWidth` everywhere and no heading, button, timer chip, `.chat-head h2`, `.trust-actions` or `.flow` element has `scrollWidth` above `clientWidth`; a 70-character unbroken word wraps inside its bubble at 320. Known defect, fixed in MR 3: the guest heading (24px, -4px tracking) and the home h1 (58px, -3px) are tracked too tight and their letters touch. |
| TYPE-03 | Search the home, chat and sheet for the `↗` glyph, in a desktop browser and in iOS Safari. | It appears nowhere. The actions read "Create room", "Copy link" and "End the chat for both"; no blue emoji arrow on iOS. |
| TYPE-04 | Send a message as the creator and wait for it to be accepted. | A drawn check with the accessible name "Sent" sits next to the time, vertically centred on it. Pending messages show "Sending…"; failed ones "Not sent". Verified at desktop (1280) only; not walked on mobile. |
| TYPE-05 | Leave the composer empty and measure the send button. | It is disabled and neutral (line-colour border, muted icon, opacity 1). Its border has at least 3:1 contrast against the composer background. |
| TYPE-06 | In a creator tab inside a room, set `location.hash` to an empty fragment. Then set it to a fresh `#invite=...&k=...` from another room. | The empty fragment does not reload and the chat stays. The new invite reloads the tab into the guest screen for that invite. Then call `history.back()`: the tab returns to `#room=...` (a reload), restores the original room from session storage, reconnects and the timer continues; no data loss and no broken state. |
| TYPE-07 | Select text and focus an input. | The selection uses the accent tint with readable text and the caret is the accent colour. Walked in a browser: selecting the home h1 shows a lime highlight with dark text (screenshot); `caretColor` of `#title` and `#message` computes to rgb(204, 248, 139) = `--accent`. |

### Mobile chat shell

Run in a browser at `375x812` and `320x700` with two participants (creator and guest via the invite link). Desktop (above 700px) must look and behave as before.

| ID | Preconditions and steps | Expected result |
|---|---|---|
| MOBILE-01 | In a room with 30 messages, scroll the message list and try to scroll the page. | Only `.messages` scrolls. `document.scrollingElement.scrollHeight <= innerHeight`; the head, status row and composer do not move. |
| MOBILE-02 | Tap ⋯ in the head. | The sheet opens with `role="dialog"`, `aria-modal="true"`, `aria-expanded="true"` on ⋯ and focus on ×. `.conversation` and `.chat-head` are `inert`; the backdrop is visible. |
| MOBILE-03 | Close the sheet with Esc, with ×, and with a backdrop tap (three runs). | The sheet closes, `inert` and the dialog attributes are removed, and focus returns to ⋯. |
| MOBILE-04 | Open the sheet, tap "End the chat for both", then Cancel. | The end dialog closes, the sheet stays open and focus is on `#end-button`. |
| MOBILE-05 | Open the sheet, tap End, then "End for both". | The room ends: the head chip reads "Ended" without the `.ending` colour, ⋯ is hidden, the ended panel is shown and the sheet is closed. On mobile the ended panel fills the body (`#chat.ended .messages-wrap` is hidden and the panel is centred between the status row and the bottom). |
| MOBILE-06 | With the sheet open on the guest, end the room from the creator. | The guest's sheet closes (not inert, no backdrop), the chip reads "Ended", and focus moves to the ended title. |
| MOBILE-07 | In `verify` with the trust strip open, focus the composer, then blur it. | While focused the strip is hidden and the badge stays; on blur the strip returns. `state.e2ee.verified` and `state.trustOpen` do not change. Walked with real clicks (programmatic `focus()` in an unfocused tab does not fire `focusin`). This case does not cover the on-screen keyboard; see MOBILE-13. |
| MOBILE-08 | Open the sheet at ≤ 700px, then widen the viewport past 700px. | The sheet closes without moving focus; `inert`, backdrop and dialog attributes are gone and the desktop sidebar is shown. |
| MOBILE-09 | Measure the minimum computed `font-size` over all visible text inside `#chat` at 375 and 320 widths, with the sheet closed and open. | The minimum is at least 12px. |
| MOBILE-10 | Open the chat at 1280px wide. | The desktop layout is unchanged: no `.chat-head`, sidebar visible, site header and footer shown, no ⋯, no backdrop. |
| MOBILE-11 | At 320x700, open the sheet and measure it. | `getBoundingClientRect().height` is below `innerHeight` (max 85%), `#end-button` is inside the viewport (reachable, by scrolling inside the sheet if it overflows), no horizontal overflow. Walked: 1 participant 538px, 2 participants 584px of 700px, End at bottom 676px. |
| MOBILE-12 | Watch the browser console during MOBILE-01 to MOBILE-11. | No console errors. |
| MOBILE-13 | On a real iPhone (Safari) and an Android phone (Chrome), focus the composer so the on-screen keyboard opens. | The composer stays above the keyboard; on iOS `--app-height` and `top` on `#chat` follow `visualViewport` height and offsetTop; a list that was at the bottom stays at the bottom after the resize; on Android Chrome `interactive-widget=resizes-content` shrinks the layout viewport. iOS: verified 2026-11-22 in the iOS Simulator (iPhone 17, iOS 27.2, Safari) with the software keyboard: composer and send button above the keyboard, head and badge in place, list kept at the last message, a typed message sent with Return. Fields are 16px at ≤700px so Safari does not zoom on focus (it did at 13px, pushing the badge, send button and ⋯ off screen). Android: not verified (no emulator). |
| MOBILE-14 | Enter a room (create or join) at ≤ 700px. | Focus lands on `#chat-head-title` (on desktop, on `#room-title`). Walked at 375 and 320 (`document.activeElement.id`); also pinned by a source test. |
| MOBILE-15 | At ≤ 700px with the sheet closed and open, check where `#timer-alert` is. | It is a child of `#chat`, outside the sheet, so the one-minute warning is announced while the sheet is closed. Walked for placement only (`#timer-alert` parent is `chat`, `room-sheet.contains` is false); the announcement itself at one minute was not exercised. |
| MOBILE-16 | Hide the chat again (for example `restoreFailed()`). | The `in-chat` class is removed from `body`, so the home page keeps its header. Source test only (`TestFrontendMobileShellLayout`); not walked. |

## Report template

```text
Date and commit:
Environment and browser:

| ID | Result | Comment / defect |
|---|---|---|
| UI-01 | PASS | |

Summary:
Blocking defects:
Run limitations:
```
