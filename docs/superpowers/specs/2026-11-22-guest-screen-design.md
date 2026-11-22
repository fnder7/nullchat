# Guest screen

Date: 2026-11-22 · Status: approved · Branch: `feat/guest-screen` (based on `feat/trust-strip`, MR !9)

## Intent

The design critique of 2026-11-22 (`.impeccable/critique/2026-11-22T13-43-42Z__web-index-html.md`) found two P1 problems on the guest's side:

- **The guest lands on the creator's marketing page.** Opening an invite link shows "Just talk.", the 01/02/03 flow and the principles strip. On mobile the Join button sits about 1.2 screens down. Nothing tells the guest what the link is, what the server can see, or why a password might be needed.
- **A link that no longer works still invites them in.** The card says "You are invited" and the Join button is live. Only after a click does a message appear under the button, and the card around it still looks like a valid invitation.

The fix is a dedicated guest screen (approach 1, chosen by the user). It has a clear terminal state for a link that cannot be opened.

**Anonymity rule (from the user).** nullchat is anonymous. The server must not tell a link holder whether the invite was used, revoked, or the room is over. All three stay one response, `410 room_unavailable`, as today. Splitting them (approach B) was rejected for this reason. The guest screen therefore shows **one** terminal state for every server-side "can't open", and its copy covers all causes without claiming which one applies.

## Goals

- An invite URL opens a dedicated screen: no creator hero, no principles strip, and the Join action within the first mobile viewport.
- The screen says in plain words what the link is (one seat, works once), what happens on joining, and what the server sees (only ciphertext).
- Pressing Enter in the password field submits.
- A wrong password is shown at the field, not under the button, and the field is marked invalid.
- A link that cannot be opened ends in one calm terminal state. There is no Join button, and there is a clear next step.
- Problems the browser can detect on its own are shown at once, before any request: a damaged link, or a page not on HTTPS.

## Non-goals

- Any server or protocol change. `join` keeps its responses and error codes.
- A pre-join preview (time left, title).
- Telling the creator anything new about failed joins.
- The mobile app shell for the chat (the next project).
- Changes to the creator's home screen beyond removing the invite panel from its card.

## Anti-goals

- The terminal copy implies a specific cause ("someone used your link") that the server deliberately does not reveal.
- The guest screen reads anything beyond the URL fragment and the join response.
- The screen looks like the creator's page with parts hidden.

## Constraints

- Vanilla JS/HTML/CSS, no `innerHTML` (CSP with Trusted Types). The visual language and tokens of `web/style.css` stay. Text is at least 12px, contrast at least 4.5:1.
- Server responses relevant here, unchanged: `join_proof_invalid` (403, wrong password or damaged secret; the 5th failure also revokes the invite), `room_unavailable` (410: used, revoked, closed, expired or unknown), `invalid_input` (400), and transient ones (`rate_limited`, `database_unavailable`, `capacity_reached`).
- The invite format is `#invite=<64-char token>&k=<base64url 32-byte secret>[&pw=1]` (SPEC.md §2).

## Approaches

**Backend.**
- A, unchanged (chosen after the user's anonymity rule).
- B, split error codes. It was picked first, then rejected: it tells a link holder whether the invite was used.
- C, preview endpoint. Rejected: it adds a new API and exposes metadata before joining.

**Frontend (decided by the user: 1).**
- **1, a separate `<main id="invite">` screen (chosen).** A clean guest-only layout with room for the terminal states.
- 2, reuse `#home` with a body class. Less markup moves, but the terminal state is cramped in the create card, and guest and creator logic stay tangled.

## Design

`<main id="invite" class="invite-screen" hidden>` sits beside `#home` and `#chat`. The boot code shows it instead of `#home` when the fragment has `invite`. The create card loses `#invite-panel`, and `#home` no longer branches on `invite`.

The screen is one centred column: max-width close to today's create card, stacked on mobile. It has one `<section>` whose content follows the state.

| State | When | Content |
|---|---|---|
| `ready` | on load, when the link is well-formed and WebCrypto is available | Heading **"You're invited to a private conversation"**. Text: "This link holds one seat and works once. When you join, the room opens for the two of you." A short list of three facts: "Messages are encrypted in your browser. The server only sees ciphertext." · "The room closes by itself when its time runs out." · "No sign-up, no profile." If `pw=1`: a password field labelled "Room password", with the hint "The person who invited you should send it separately from the link." Primary button **"Join the conversation"**. Text link "Or create your own room", pointing to `/`. |
| `joining` | after submit, until the response | The same content. The button is disabled and reads "Joining…". A polite status says "Joining the conversation…". |
| field error | `join_proof_invalid`, or an empty password | Stays in `ready`. The message sits under the password field. The field gets `aria-invalid="true"` and `aria-describedby` pointing at the message, and focus returns to the field. For a wrong password: "Wrong password, or the link is damaged. After 5 wrong attempts the link stops working." For an empty field: "Enter the room password." For a link without `pw=1` there is no field, so the same text goes under the button. |
| `closed` | `room_unavailable` | Neutral tone. Heading **"This link can't be opened"**. Text: "It may have been used already, turned off after wrong passwords, or the conversation is over. For privacy, nullchat doesn't say which." A second line: "If you haven't opened this link before, tell the person who sent it, using a different app, and ask for a new one." Button "Create your own room". |
| `damaged` | on load, when `invite` is not 64 characters or `k` does not decode to 32 bytes; also on `invalid_input` from join | Neutral tone. Heading **"This link is incomplete"**. Text: "Part of the link is missing. Ask for the full link again, and copy it in one piece." Button "Create your own room". |
| `insecure` | on load, when there is no `crypto.subtle` | Neutral tone. Heading **"Open this link over HTTPS"**. Text: "Encryption only works on a secure connection. Open the link at its https:// address." No button. |
| transient error | `rate_limited`, `database_unavailable`, `capacity_reached`, a network failure, any code not listed above (for example `room_full`), a `checkStorage()` failure, or an error thrown by `enter()` | Stays in `ready`. The message sits under the button: text from today's `errors` map, falling back to the thrown message. The button is re-enabled for a retry with the same prepared request. |

Behaviour:
- The password field and the Join button are a real `<form id="join-form">`. Submit handles both Enter and click. Changing the password resets the prepared request, as today.
- Entering a terminal state (`closed`, `damaged`, `insecure`) replaces the section's content and moves focus to its heading (`tabindex="-1"`). Entering `ready` does not move focus. Field errors move focus to the field.
- A successful join calls today's `enter()` and hides `#invite`. The fragment changes from `#invite=…` to `#room=…`, as today.
- "Create your own room" is a link to `/`.
- The brand header and the footer stay.
- The `room_unavailable` text in the `errors` map, which other screens use, is unchanged.

## Left to the builder

- Layout details within the existing tokens: column width and spacing. The neutral tone uses the panel and line tokens.
- Whether the state logic lives in a small pure helper (as `trust.js` does for the strip) or inline in `app.js`. Suggested: a pure `inviteState(...)` with node tests, for the load-time link checks and the response → state mapping.
- Test names.
