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
