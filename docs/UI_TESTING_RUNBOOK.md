# UI testing runbook for agents

Instructions for a repeatable manual UI/E2E run of
[`UI_TEST_CASES.md`](UI_TEST_CASES.md). It does not replace the Go and PostgreSQL
integration tests: the UI run checks the user-facing contract, the automated suites
check invariants and concurrent scenarios.

## 1. Record the initial state

```sh
git status --short
git rev-parse --short HEAD
```

Do not modify or delete existing user files. In the report, state the commit,
browser, date and any changes already present in the worktree.

## 2. Bring up the environment

Create a separate development secret and do not print or commit it:

```sh
export IDEMPOTENCY_ENCRYPTION_KEY="$(openssl rand -hex 32)"
docker compose up --build --abort-on-container-exit migrate
docker compose up --build -d postgres nullchat
curl --fail http://127.0.0.1:8080/readyz
```

Do not run `docker compose down -v`: it deletes the test data in the volume. The
run uses only the local development DB from `compose.yaml`.

## 3. Prepare the browser

- Open `http://127.0.0.1:8080` in Chrome DevTools MCP.
- Open the creator, the guest and the third user in separate isolated browser
  contexts. Different tabs of one context can share storage and are not suitable
  for checking independent participants.
- Before the negative scenarios, check for a clean console and successful loading
  of the HTML, CSS, JS and favicon.
- For every significant step, record the actual visible text, button states, the
  message counter and the connection status.

## 4. Recommended order

1. Run P0 `UI-01`–`UI-05` in a 15-minute room.
2. In the same room, run offline/reconnect and the manual retry (`UI-06`,
   `NET-01`–`NET-05`). Network emulation alone may not close an already open
   WebSocket; after enabling Offline, force-close the test socket or stop the
   backend.
3. Restart only the backend and check persistence (`UI-07`, `NET-06`):

   ```sh
   docker compose restart nullchat
   ```

4. Stop PostgreSQL, check the create/send error, then bring the DB back and
   repeat the operation (`ROOM-05`, `NET-07`, `NET-08`):

   ```sh
   docker compose stop postgres
   docker compose up -d postgres
   docker compose ps
   ```

5. End a room as the guest, then a separate room as the creator (`END-01`,
   `END-02`). Reload the page and retry the invite after the end.
6. Create a separate room with a 1-minute TTL. Wait for the server event; do not
   fake it by changing the browser clock (`END-05`–`END-07`).
7. Run the validation, XSS, storage, privacy and responsive cases.
8. Check the `390x844` viewport and desktop. Compare
   `document.documentElement.scrollWidth` with `innerWidth`.
9. Run a Lighthouse snapshot of the home page and record the names of all
   failed accessibility audits.

## 5. Limit scenarios

Do not run `MSG-11` by sending 499 messages with pauses: the rate limit makes the
run long and flaky. Preparing a fixture directly in the development PostgreSQL is
acceptable:

1. Create a separate room through the UI and record its `room_id` and
   `participant_id`, without the session/invite token, in the report.
2. Insert 499 test messages with sequences `1..499`, set
   `rooms.message_count = 499`, `next_message_sequence = 500` and
   `sessions.last_send_at = NULL`.
3. Reload the UI: it must show `499 / 500` and 499 messages.
4. Send the 500th message through the UI, then, after the rate-limit interval,
   the 501st.
5. Expect `500 / 500` and an unsent 501st message with the reason
   `message_limit`.

The fixture is created only in the local development DB. Never run this SQL
against production or a shared DB.

Create/join idempotency can be checked from the page by repeating an identical
fetch with the same `Idempotency-Key`. In the report, compare only room/session
equality; do not print the session and invite tokens themselves.

## 6. Privacy anti-enumeration

- Check an existing and a random `#room=<64 hex>` in clean contexts without
  `sessionStorage`.
- Compare not only the text but also Network: opening `#room` without a session
  must not contact the backend about the room.
- For invites, compare a used, a closed and a random 64-character hex token. All
  must give the same public state, without the title, participants or any other
  room data.
- A successful join with a valid one-time invite is not a leak: possession of the
  invite grants the intended access.

## 7. Automated baseline

After the UI run, execute:

```sh
go test -race ./...
go vet ./...
node --check web/app.js
docker compose run --rm test
```

Without `TEST_DATABASE_URL`, the local PostgreSQL tests may be skipped, so the
Compose test service is mandatory for a full run.

## 8. Report and wrap-up

- Create `docs/test-reports/YYYY-MM-DD-ui-regression.md` from the template in
  `UI_TEST_CASES.md`.
- Every case must have `PASS`, `FAIL` or `BLOCKED`. Do not count an unchecked
  case as passed on the basis of code or unit tests.
- For `FAIL`, record the expected and actual result, the minimal steps and the
  affected page.
- Separate the expected console/network errors of negative scenarios from
  unhandled application errors.
- Leave `postgres` and `nullchat` running only if that helps the next developer;
  state their status explicitly. Do not delete the volume without a direct
  request from the user.
