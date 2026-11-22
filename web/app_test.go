package web

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestFrontendStaticAccessibilityContract(t *testing.T) {
	html, err := assets.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `aria-label="n_nullchat. — home"`) {
		t.Error("brand accessible name does not include its visible text")
	}

	css, err := assets.ReadFile("style.css")
	if err != nil {
		t.Fatal(err)
	}
	style := string(css)
	if !strings.Contains(style, "--muted-subtle:#84907f") {
		t.Error("subtle text color token is missing")
	}
	for _, selector := range []string{
		"label span",
		"input::placeholder,textarea::placeholder",
		".card-footnote",
	} {
		t.Run(selector, func(t *testing.T) {
			declaration := regexp.MustCompile(regexp.QuoteMeta(selector) + `\s*\{[^}]*color:\s*var\(--muted-subtle\)\s*;`)
			if !declaration.MatchString(style) {
				t.Errorf("%s does not use the subtle text color token", selector)
			}
		})
	}
}

func TestFrontendRetriesTransientWebSocketCloseWithoutDeletingSession(t *testing.T) {
	if output, err := exec.Command("node", "--check", "app.js").CombinedOutput(); err != nil {
		t.Fatalf("frontend syntax: %v\n%s", err, output)
	}
	source, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "if (event.code === 1008) { finish") {
		t.Fatal("permanent WebSocket authentication close no longer finishes the session")
	}
	if !strings.Contains(text, "if (state.ended || state.socket !== ws) return;") {
		t.Fatal("a socket replaced by resync is treated as a lost connection")
	}
	if !strings.Contains(text, "if (payload.sequence > state.lastSequence + 1) { resync(); return; }") {
		t.Fatal("a gap in message sequence does not trigger a resync")
	}
	if !strings.Contains(text, "if (event.code === 1013)") {
		t.Fatal("database-unavailable close is not surfaced as retryable")
	}
	closeHandler := text[strings.Index(text, "ws.onclose ="):]
	if strings.Contains(closeHandler, "sessionStorage.removeItem") {
		t.Fatal("retryable close handler deletes persisted credentials")
	}
}

func TestFrontendUsesKnownRetryableWebSocketCloseReasonWithDatabaseFallback(t *testing.T) {
	source, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "showError('chat-error', errors[event.reason] || errors.database_unavailable);") {
		t.Fatal("retryable WebSocket close reason is overwritten or lacks database-unavailable fallback")
	}
}

func TestFrontendSuccessfulManualRetryClearsStaleError(t *testing.T) {
	source, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "$('messages').addEventListener('click'")
	end := strings.Index(text, "$('message-form').addEventListener('submit'")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("could not locate manual retry handler boundaries")
	}
	handler := text[start:end]
	// A retried message is resealed with a fresh ID and counter (a failed send
	// was never stored) and goes through the ordered send queue.
	for _, required := range []string{"entry.client_message_id = crypto.randomUUID();", "entry.ciphertext = '';", "showError('chat-error');", "enqueue(entry);"} {
		if !strings.Contains(handler, required) {
			t.Errorf("manual retry handler lacks %q", required)
		}
	}
	queue := text[strings.Index(text, "function enqueue("):]
	queue = queue[:strings.Index(queue, "\nfunction ")]
	if !strings.Contains(queue, "if (transmit(entry, socket))") || !strings.Contains(queue, "else showError('chat-error', 'Message not sent. Press “Retry”.');") {
		t.Fatal("send queue does not surface failed transmissions")
	}
}

func TestFrontendCopyMatchesPersistenceContract(t *testing.T) {
	source, err := assets.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "History is available to participants until the room closes. End-to-end encrypted: the server sees only ciphertext.") {
		t.Fatal("footer does not describe the persistence and encryption contract")
	}
	if strings.Contains(text, "No E2EE") {
		t.Fatal("footer still claims there is no end-to-end encryption")
	}
	if strings.Contains(text, "history is kept in memory") {
		t.Fatal("footer still describes in-memory history")
	}
}

func TestFrontendUsesOperationSpecificErrors(t *testing.T) {
	source, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		"async function api(path, data, token, key = '', errorOverrides = {})",
		"errorOverrides[result.error] || errors[result.error] || 'The request failed. Please try again.'",
		"api('/api/v1/rooms', request.body, '', request.key, { database_unavailable: 'The server is temporarily unavailable. The room was not created, please try again.' })",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("frontend lacks operation-specific error contract: %q", required)
		}
	}
}

func TestFrontendRoomURLWithoutSessionUsesNeutralCopy(t *testing.T) {
	source, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "The room could not be opened. Use a valid invite or create a new room.") {
		t.Fatal("room URL without local credentials lacks neutral recovery copy")
	}
	if strings.Contains(text, "This tab has no session for the room") {
		t.Fatal("room URL still exposes missing sessionStorage as the recovery copy")
	}
}

// Retrying create/join with the same Idempotency-Key must resend the identical
// encrypted body; re-encrypting would conflict or replay keys the tab lacks.
func TestFrontendReusesPreparedIdempotentRequests(t *testing.T) {
	source, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		"if (prepared.create) return prepared.create;",
		"if (prepared.join) return prepared.join;",
		"for (const id of ['title', 'ttl', 'password']) $(id).addEventListener('input', () => { prepared.create = null; });",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("frontend lacks prepared-request contract: %q", required)
		}
	}
}

func TestFrontendKeepsChatUsableWithKeyboardAndScreenReader(t *testing.T) {
	source, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	html, err := assets.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	css, err := assets.ReadFile("style.css")
	if err != nil {
		t.Fatal(err)
	}

	// The textarea drops its own outline, so the composer must show focus instead.
	if !regexp.MustCompile(`\.composer:focus-within\s*\{[^}]*outline:\s*2px solid var\(--accent\)`).Match(css) {
		t.Error("composer has no visible focus indicator")
	}

	// #messages is a live region: rebuilding it re-announces the whole history.
	render := text[strings.Index(text, "function renderMessages("):]
	render = render[:strings.Index(render, "\n}\n")]
	if strings.Contains(render, "replaceChildren") {
		t.Error("renderMessages rebuilds the live message log instead of reusing nodes")
	}
	if !strings.Contains(render, "patchNode(node, fresh)") {
		t.Error("renderMessages does not patch existing message nodes in place")
	}

	// A failed key check must reach assistive technology through an alert.
	if !strings.Contains(string(html), `<p id="safety-alert" class="sr-only" role="alert"></p>`) {
		t.Error("key failure alert region is missing")
	}
	failure := text[strings.Index(text, "function keyFailure("):]
	failure = failure[:strings.Index(failure, "\n}\n")]
	if !strings.Contains(failure, "$('safety-alert').textContent =") {
		t.Error("keyFailure does not announce the failure")
	}

	// Focus must follow the screen that replaces the one holding it.
	for id, call := range map[string]string{"room-title": "$('room-title').focus();", "ended-title": "$('ended-title').focus();"} {
		if !strings.Contains(string(html), `id="`+id+`" tabindex="-1"`) {
			t.Errorf("#%s cannot receive programmatic focus", id)
		}
		if !strings.Contains(text, call) {
			t.Errorf("focus is never moved to #%s", id)
		}
	}
}

// Message text is untrusted: links must be built as nodes and open detached from this tab.
func TestFrontendBuildsMessageLinksSafely(t *testing.T) {
	source, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if strings.Contains(text, "innerHTML") || strings.Contains(text, "insertAdjacentHTML") {
		t.Error("frontend writes markup instead of building nodes")
	}
	fill := text[strings.Index(text, "function fillText("):]
	fill = fill[:strings.Index(fill, "\n}\n")]
	for _, required := range []string{"linkify(text)", "a.textContent = segment.text", "a.rel = 'noopener noreferrer nofollow'", "a.target = '_blank'"} {
		if !strings.Contains(fill, required) {
			t.Errorf("fillText lacks %q", required)
		}
	}
}

// Notifications live in the OS history, outside end-to-end encryption: they may name
// the sender but never carry message text or the encrypted room title.
func TestFrontendNotificationsCarryNoPlaintext(t *testing.T) {
	source, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, name := range []string{"function notify(", "function noteIncoming("} {
		body := text[strings.Index(text, name):]
		body = body[:strings.Index(body, "\n}\n")]
		for _, leak := range []string{"state.plain", ".text", "state.title"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s reads %s", name, leak)
			}
		}
	}
	if !strings.Contains(text, "new Notification('nullchat', { body, tag:") {
		t.Error("notification title is not the fixed app name")
	}
	if !strings.Contains(text, "if (document.hidden) {") {
		t.Error("notifications are not limited to a background tab")
	}
}

func TestFrontendEndsChatThroughDialog(t *testing.T) {
	source, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	html, err := assets.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if strings.Contains(text, "confirm(") {
		t.Error("app.js still uses confirm()")
	}
	if !strings.Contains(string(html), `<dialog id="end-dialog"`) {
		t.Error("index.html has no end dialog")
	}
	page := string(html)
	chatStart := strings.Index(page, `<main id="chat"`)
	dialog := strings.Index(page, `<dialog id="end-dialog"`)
	chatEnd := chatStart + strings.Index(page[chatStart:], "</main>")
	if chatStart < 0 || dialog < chatStart || dialog > chatEnd {
		t.Error("end dialog is not inside <main id=\"chat\">")
	}
	if !strings.Contains(page, `<button id="end-cancel" class="secondary" type="button" autofocus>`) {
		t.Error("end-cancel button lacks autofocus")
	}
	for _, want := range []string{"function openEndDialog(", ".showModal()"} {
		if !strings.Contains(text, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	body := text[strings.Index(text, "function finish("):]
	body = body[:strings.Index(body, "\n}\n")]
	if !strings.Contains(body, "$('end-dialog').close()") {
		t.Error("finish() does not close the end dialog")
	}
	for _, copy := range []string{
		"End the conversation? The room and its history will be gone for both of you.",
		"The codes don't match. The person on the other end may not be your contact. End the conversation for both?",
		"Key verification failed, so this conversation is not safe. End it for both?",
	} {
		if !strings.Contains(text, copy) {
			t.Errorf("END_COPY lacks %q", copy)
		}
	}
}

func TestFrontendInviteLivesInTrustStrip(t *testing.T) {
	htmlBytes, err := assets.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(htmlBytes)
	for _, want := range []string{`id="trust-strip"`, `id="trust-title" tabindex="-1"`, `id="trust-badge"`, `aria-controls="trust-strip"`, `id="trust-status" class="sr-only" role="status"`} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	if strings.Contains(page, `id="share-panel"`) {
		t.Error("index.html still has the share panel")
	}
	strip := strings.Index(page, `id="trust-strip"`)
	wrap := strings.Index(page, `class="messages-wrap"`)
	wrapEnd := wrap + strings.Index(page[wrap:], `id="chat-error"`)
	if strip >= wrap && strip < wrapEnd {
		t.Error("trust strip is inside the messages wrapper")
	}
	js, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(js)
	for _, want := range []string{"from './trust.js'", "navigator.share", "navigator.clipboard.writeText",
		"Send this link to your contact",
		"Anyone who opens this link takes the seat. Send it somewhere private.",
		"Send the password separately.",
		"The invite was revoked after 5 wrong password attempts. End the chat and create a new room.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	branch := text[strings.Index(text, "type === 'invite_revoked'"):]
	branch = branch[:strings.Index(branch, "\n")+200]
	branch = branch[:strings.Index(branch, "room_expired")]
	if strings.Contains(branch, "showError('chat-error'") {
		t.Error("invite_revoked still reports through chat-error")
	}
}

func funcBody(t *testing.T, text, name string) string {
	t.Helper()
	i := strings.Index(text, name)
	if i < 0 {
		t.Fatalf("app.js lacks %s", name)
	}
	body := text[i:]
	return body[:strings.Index(body, "\n}\n")]
}

func TestFrontendInviteHasItsOwnScreen(t *testing.T) {
	htmlBytes, err := assets.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(htmlBytes)
	if !strings.Contains(html, `<main id="invite"`) {
		t.Fatal(`index.html lacks <main id="invite"`)
	}
	if strings.Contains(html, `id="invite-panel"`) {
		t.Error("the create card still carries #invite-panel")
	}
	home := strings.Index(html, `<main id="home"`)
	homeEnd := home + strings.Index(html[home:], "</main>")
	if invite := strings.Index(html, `<main id="invite"`); invite < homeEnd {
		t.Error("#invite sits inside #home")
	}
	if !strings.Contains(html, `<h1 id="invite-title" tabindex="-1"`) {
		t.Error("#invite-title is not focusable with tabindex=-1")
	}
	if !strings.Contains(html, `<form id="create-form">`) {
		t.Error("create form changed or left #home")
	}
	jsBytes, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsBytes)
	for _, required := range []string{
		"from './invite.js'", "function renderInvite(",
		"You're invited to a private conversation",
		"This link holds one seat and works once. When you join, the room opens for the two of you.",
		"Messages are encrypted in your browser. The server only sees ciphertext.",
		"The room closes by itself when its time runs out.",
		"No sign-up, no profile.",
		"Room password",
		"The person who invited you should send it separately from the link.",
		"Join the conversation", "Or create your own room",
		"This link can't be opened",
		"It may have been used already, turned off after wrong passwords, or the conversation is over. For privacy, nullchat doesn't say which.",
		"If you haven't opened this link before, tell the person who sent it, using a different app, and ask for a new one.",
		"Create your own room",
		"This link is incomplete",
		"Part of the link is missing. Ask for the full link again, and copy it in one piece.",
		"Open this link over HTTPS",
		"Encryption only works on a secure connection. Open the link at its https:// address.",
	} {
		if !strings.Contains(js, required) {
			t.Errorf("app.js lacks guest copy %q", required)
		}
	}
	render := funcBody(t, js, "function renderInvite(")
	for _, required := range []string{"type = 'submit'", "join-form"} {
		if !strings.Contains(render, required) {
			t.Errorf("renderInvite lacks %q", required)
		}
	}
	if !strings.Contains(funcBody(t, js, "async function enter("), "$('invite').hidden = true") {
		t.Error("enter() does not hide #invite")
	}
	for _, gone := range []string{"$('join-button').addEventListener", "params.has('invite')) {\n    $('create-form')", "invite-panel"} {
		if strings.Contains(js, gone) {
			t.Errorf("app.js still has the old invite flow: %q", gone)
		}
	}
}

func TestFrontendJoinFlowOnGuestScreen(t *testing.T) {
	source, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(source)
	if !strings.Contains(funcBody(t, js, "async function api("), ".code = result.error") {
		t.Error("api() does not attach the server code to the error")
	}
	submit := funcBody(t, js, "async function submitJoin(")
	if !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(submit, "async function submitJoin(event) {")), "event.preventDefault();") {
		t.Error("submitJoin must call event.preventDefault() first")
	}
	for _, required := range []string{
		"button.disabled) return",
		"'Joining…'", "Joining the conversation…",
		"api('/api/v1/join', request.body, '', request.key)",
		"joinOutcome(", "renderInvite('closed')", "renderInvite('damaged')", "showJoinFieldError(",
		"password_empty", "Wrong password, or the link is damaged. After 5 wrong attempts the link stops working.", "Enter the room password.",
	} {
		if !strings.Contains(submit, required) {
			t.Errorf("submitJoin lacks %q", required)
		}
	}
	if strings.Count(submit, "prepared.join = null") != 1 {
		t.Error("prepared.join must be cleared exactly once, after success, never on a transient error")
	}
	if strings.Index(submit, "prepared.join = null") < strings.Index(submit, "await enter(") {
		t.Error("prepared.join is cleared before enter() resolved; a failed enter() must keep the idempotency key")
	}
	if strings.Count(submit, "showError('join-field-error')") != strings.Count(submit, "if ($('join-field-error')) showError('join-field-error')") {
		t.Error("#join-field-error exists only with pw=1; clearing it in submitJoin must be guarded")
	}
	for _, required := range []string{"removeAttribute('aria-invalid')", "readOnly = true", "readOnly = false"} {
		if !strings.Contains(submit, required) {
			t.Errorf("submitJoin lacks %q", required)
		}
	}
	field := funcBody(t, js, "function showJoinFieldError(")
	for _, required := range []string{"join-field-error", "aria-invalid", "aria-describedby", "focus()", "join-error"} {
		if !strings.Contains(field, required) {
			t.Errorf("showJoinFieldError lacks %q", required)
		}
	}
	render := funcBody(t, js, "function renderInvite(")
	for _, required := range []string{"addEventListener('submit', submitJoin)", "prepared.join = null", "removeAttribute('aria-invalid')", "join-field-error"} {
		if !strings.Contains(render, required) {
			t.Errorf("renderInvite lacks %q", required)
		}
	}
	if !strings.Contains(render, "fieldError.setAttribute('role', 'alert')") {
		t.Error("#join-field-error needs role=alert so the error is announced")
	}
	if css, err := assets.ReadFile("style.css"); err != nil || !strings.Contains(string(css), `#join-password[aria-invalid="true"]`) {
		t.Error("style.css lacks the invalid style for #join-password")
	}
	if !strings.Contains(js, "import { linkState, joinOutcome } from './invite.js'") {
		t.Error("app.js does not import joinOutcome")
	}
	prep := funcBody(t, js, "async function prepareJoin(")
	if !strings.Contains(prep, "$('join-password')?.value") || !strings.Contains(prep, "'password_empty'") {
		t.Error("prepareJoin must read the optional field and flag an empty password with code password_empty")
	}
}

func TestFrontendVerifiesSafetyCodeLocally(t *testing.T) {
	js, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	htmlBytes, err := assets.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	text, page := string(js), string(htmlBytes)

	set := funcBody(t, text, "function setVerified(")
	if !strings.Contains(set, "state.e2ee.verified") || !strings.Contains(set, "save()") {
		t.Error("setVerified does not persist state.e2ee.verified through save()")
	}
	if strings.Contains(funcBody(t, text, "function openEndDialog("), "setVerified") {
		t.Error("openEndDialog must not mark anything verified")
	}
	for _, want := range []string{"openEndDialog('mismatch'", "openEndDialog('broken'"} {
		if !strings.Contains(text, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	// The mismatch handler may only open the dialog: cancelling must leave verified unset.
	m := text[strings.Index(text, "openEndDialog('mismatch'"):]
	m = m[:strings.Index(m, "\n")]
	if strings.Contains(m, "setVerified") {
		t.Error("the mismatch handler sets verified")
	}

	render := funcBody(t, text, "function renderTrust(")
	for _, want := range []string{"contains(document.activeElement)", "$('trust-title').focus()",
		"!state.safetyCode", "Your contact joined. Compare the safety code."} {
		if !strings.Contains(render, want) {
			t.Errorf("renderTrust lacks %s", want)
		}
	}
	// Transient hidden: two people but no code yet keeps whatever is shown, so the strip
	// goes straight from invite to verify and the focus rule fires once.
	guard := regexp.MustCompile(`(?s)!state\.ended && !state\.keyError && state\.room\.participants\.length > 1 && !state\.safetyCode\) return`)
	if !guard.MatchString(render) {
		t.Error("renderTrust does not keep the current strip while the code is being derived")
	}

	if !strings.Contains(text, "setAttribute('aria-label', codeLabel(") {
		t.Error("the safety code has no aria-label from codeLabel")
	}
	for _, gone := range []string{"safety-code", "safety-note"} {
		if strings.Contains(text, gone) || strings.Contains(page, gone) {
			t.Errorf("%s is still referenced", gone)
		}
	}
	if !strings.Contains(text, "const KEY_NOTE") || strings.Count(text, "KEY_NOTE") < 3 {
		t.Error("the key failure note is not one shared constant")
	}
	alert := `<p id="safety-alert" class="sr-only" role="alert"></p>`
	strip := page[strings.Index(page, `id="trust-strip"`):]
	strip = strip[:strings.Index(strip, "</section>")]
	if !strings.Contains(strip, alert) {
		t.Error("the key failure alert is not inside the trust strip")
	}
	status := `<p id="trust-status" class="sr-only" role="status"></p>`
	head := page[strings.Index(page, `class="conversation-head"`):]
	head = head[:strings.Index(head, `id="trust-strip"`)]
	if strings.Contains(strip, status) || !strings.Contains(head, status) {
		t.Error("#trust-status must sit in .conversation-head, outside the strip that collapses")
	}
	if !strings.Contains(text, "trustButton('It matches', 'primary trust-primary'") {
		t.Error("It matches is not the primary button")
	}
	if strings.Contains(funcBody(t, text, "function buildVerified("), "state.trustOpen = true") {
		t.Error("Compare again leaves trustOpen stuck")
	}
	panel := page[strings.Index(page, `<section id="safety-panel"`):]
	panel = panel[:strings.Index(panel, "</section>")]
	for _, want := range []string{`class="safety-panel"`, `<svg class="safety-icon" aria-hidden="true"`, `<strong>End-to-end encrypted</strong>`,
		`<p>Messages are locked in this browser before they are sent. The server only sees ciphertext.</p>`} {
		if !strings.Contains(panel, want) {
			t.Errorf("the sidebar encryption note lacks %q", want)
		}
	}
	if strings.Contains(panel, "eyebrow") {
		t.Error("the encryption note still uses an eyebrow label")
	}

	for _, copy := range []string{"Is this really them?", "Compare this code with your contact by voice or in another app. If it matches, you are talking to the right person.",
		"It matches", "It doesn't match", "Later", "You confirmed this code matches.", "Compare again", "Hide",
		"Key verification failed", "End the chat", "Marked as verified."} {
		if !strings.Contains(text, copy) {
			t.Errorf("app.js lacks %q", copy)
		}
	}
	if regexp.MustCompile(`(send|transmit)\([^)]*verified`).MatchString(text) {
		t.Error("verification is sent over the wire")
	}
	if strings.Contains(text, "if (e.name !== 'AbortError') throw e") {
		t.Error("Share rethrows non-abort errors")
	}
}

// mobileCSS returns the bodies of every @media(max-width:700px) block, joined.
func mobileCSS(t *testing.T, css string) string {
	t.Helper()
	var out strings.Builder
	rest := css
	for {
		i := strings.Index(rest, "@media(max-width:700px)")
		if i < 0 {
			return out.String()
		}
		rest = rest[i:]
		depth, end := 0, 0
		for j, c := range rest {
			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
				if depth == 0 {
					end = j
					break
				}
			}
		}
		if end == 0 {
			t.Fatal("unbalanced braces in the mobile @media block")
		}
		out.WriteString(rest[:end])
		rest = rest[end:]
	}
}

func TestFrontendMobileShellLayout(t *testing.T) {
	read := func(name string) string {
		b, err := assets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	html, js, css := read("index.html"), read("app.js"), read("style.css")

	chat := html[strings.Index(html, `<main id="chat"`):]
	head := strings.Index(chat, `<header class="chat-head">`)
	if head < 0 || head > strings.Index(chat, "room-sidebar") {
		t.Error("chat-head must be the first child of #chat, before the sidebar")
	}
	for _, want := range []string{
		`id="chat-head-title" tabindex="-1"`,
		`id="room-sheet"`,
		`interactive-widget=resizes-content`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	for id, attrs := range map[string][]string{
		"chat-head-timer": {`aria-hidden="true"`},
		"sheet-button":    {`aria-controls="room-sheet"`, `aria-label="Room details"`},
	} {
		tag := regexp.MustCompile(`<[^>]*id="` + id + `"[^>]*>`).FindString(html)
		if tag == "" {
			t.Errorf("index.html lacks #%s", id)
		}
		for _, a := range attrs {
			if !strings.Contains(tag, a) {
				t.Errorf("#%s lacks %s", id, a)
			}
		}
	}
	alert := `<p id="timer-alert" class="sr-only" role="status"></p>`
	card := html[strings.Index(html, `id="timer-card"`):]
	card = card[:strings.Index(card, "</div></div>")]
	if !strings.Contains(html, alert) || strings.Contains(card, alert) {
		t.Error("#timer-alert must live outside the timer card")
	}

	if !strings.Contains(js, "matchMedia('(max-width:700px)')") {
		t.Error("app.js has no MOBILE media query")
	}
	enter := funcBody(t, js, "async function enter(")
	for _, want := range []string{"classList.add('in-chat')", "$('chat-head-title').focus()", "$('room-title').focus()"} {
		if !strings.Contains(enter, want) {
			t.Errorf("enter() lacks %s", want)
		}
	}
	if !strings.Contains(funcBody(t, js, "function updateTimer("), "chat-head-timer") {
		t.Error("updateTimer() does not mirror into the head chip")
	}
	fin := funcBody(t, js, "function finish(")
	if !strings.Contains(fin, "'Ended'") || !strings.Contains(fin, "sheet-button") {
		t.Error("finish() must show Ended and hide the sheet button")
	}
	restore := js[strings.Index(js, "const restoreFailed"):]
	restore = restore[:strings.Index(restore, "};")]
	if !strings.Contains(restore, "classList.remove('in-chat')") {
		t.Error("restoreFailed does not drop in-chat")
	}

	m := mobileCSS(t, css)
	for _, want := range []string{"min-height:0", "var(--app-height", "body.in-chat .header", "body.in-chat footer"} {
		if !strings.Contains(m, want) {
			t.Errorf("mobile CSS lacks %s", want)
		}
	}
	if !regexp.MustCompile(`\.messages-wrap\s*\{[^}]*min-height:0`).MatchString(m) {
		t.Error("mobile .messages-wrap lacks min-height:0")
	}
	if !regexp.MustCompile(`#chat\s*\{[^}]*var\(--app-height`).MatchString(m) {
		t.Error("mobile #chat does not use --app-height")
	}
	for _, re := range []string{
		`#chat\.ended \.messages-wrap\s*\{\s*display:none`,
		`#chat \.ended-panel\s*\{[^}]*flex:1[^}]*justify-content:center[^}]*border-top:0`,
		`#chat \.trust-strip\s*\{[^}]*max-height:40%[^}]*overflow-y:auto`,
	} {
		if !regexp.MustCompile(re).MatchString(m) {
			t.Errorf("mobile CSS lacks %s", re)
		}
	}
	if !strings.Contains(funcBody(t, js, "function finish("), "$('chat').classList.add('ended')") {
		t.Error("finish() must mark #chat as ended")
	}
	if strings.Contains(m, "calc(100dvh - 245px)") || strings.Contains(m, "min-height:430px") {
		t.Error("mobile CSS keeps the old fixed conversation height")
	}
	floor := m[strings.Index(m, "Text floor"):]
	floor = floor[:strings.Index(floor, "}")]
	for _, sel := range []string{".message-note", ".jump-button", ".notify-panel p", ".avatar", ".composer-hint"} {
		if !strings.Contains(floor, "#chat "+sel) {
			t.Errorf("12px floor does not cover #chat %s with enough specificity", sel)
		}
	}
	if !strings.Contains(floor, "font-size:12px") {
		t.Error("text floor is not 12px")
	}
	if !regexp.MustCompile(`(?s)\n\.chat-head\s*\{\s*display:none`).MatchString(css) {
		t.Error(".chat-head is not hidden above 700px")
	}
	if strings.Count(funcBody(t, js, "function finish("), "$('timer-card').classList.remove('ending')") > 1 {
		t.Error("finish() clears .ending on the timer card twice")
	}
}

func TestFrontendMobileSheet(t *testing.T) {
	read := func(name string) string {
		b, err := assets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	js, css, html := read("app.js"), read("style.css"), read("index.html")
	for _, want := range []string{`id="sheet-close"`, `aria-label="Close"`, `id="sheet-backdrop"`} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	if strings.Index(html, `id="sheet-close"`) < strings.Index(html, `id="room-sheet"`) {
		t.Error("#sheet-close must be inside .room-sidebar")
	}
	open := funcBody(t, js, "function openSheet(")
	for _, want := range []string{"aria-modal", "'dialog'", "Room details", "inert = true", "$('sheet-close').focus()", "aria-expanded", "classList.add('open')"} {
		if !strings.Contains(open, want) {
			t.Errorf("openSheet lacks %s", want)
		}
	}
	cl := funcBody(t, js, "function closeSheet(")
	for _, want := range []string{"removeAttribute('aria-modal')", "removeAttribute('role')", "inert = false", "classList.remove('open')", "if (restoreFocus) $('sheet-button').focus()"} {
		if !strings.Contains(cl, want) {
			t.Errorf("closeSheet lacks %s", want)
		}
	}
	if !regexp.MustCompile(`addEventListener\('keydown'[\s\S]{0,200}'Escape'[\s\S]{0,200}closeSheet\(`).MatchString(js) {
		t.Error("Escape must close the sheet")
	}
	if !strings.Contains(js, "$('end-dialog').open") || !regexp.MustCompile(`'Escape'[\s\S]{0,120}end-dialog'\)\.open|end-dialog'\)\.open[\s\S]{0,120}'Escape'`).MatchString(js) {
		t.Error("the Escape handler must ignore the event while the end dialog is open")
	}
	if !regexp.MustCompile(`\$\('sheet-backdrop'\)\.addEventListener\('click'[^\n]*closeSheet\(`).MatchString(js) {
		t.Error("backdrop click must close the sheet")
	}
	if !regexp.MustCompile(`\$\('sheet-close'\)\.addEventListener\('click'[^\n]*closeSheet\(`).MatchString(js) ||
		!regexp.MustCompile(`\$\('sheet-button'\)\.addEventListener\('click'[^\n]*openSheet\(`).MatchString(js) {
		t.Error("sheet button and close button must open and close the sheet")
	}
	if !regexp.MustCompile(`MOBILE\.addEventListener\('change'[\s\S]{0,120}closeSheet\(\{ restoreFocus: false \}\)`).MatchString(js) {
		t.Error("leaving the mobile width must close the sheet without moving focus")
	}
	if kf := funcBody(t, js, "function keyFailure("); !strings.Contains(kf, "closeSheet()") || strings.Index(kf, "closeSheet()") > strings.Index(kf, "safety-alert") {
		t.Error("keyFailure must close the sheet before writing the alert")
	}
	if !strings.Contains(funcBody(t, js, "function finish("), "closeSheet({ restoreFocus: false })") {
		t.Error("finish() must close the sheet without restoring focus")
	}
	if strings.Contains(funcBody(t, js, "function openEndDialog("), "closeSheet") {
		t.Error("openEndDialog must not touch the sheet")
	}
	if !strings.Contains(js, "$('end-dialog').addEventListener('close', () => (endReturnTo?.isConnected ? endReturnTo : $('trust-title')).focus());") {
		t.Error("end dialog close handler must still return focus to endReturnTo")
	}
	m := mobileCSS(t, css)
	for _, want := range []string{".room-sidebar.open", "inset:auto 0 0 0", "max-height:85%", "overflow-y:auto", "transform", "#sheet-backdrop", "prefers-reduced-motion"} {
		if !strings.Contains(css, want) && !strings.Contains(m, want) {
			t.Errorf("style.css lacks %s", want)
		}
	}
	if !regexp.MustCompile(`prefers-reduced-motion[\s\S]{0,120}\.room-sidebar`).MatchString(css) {
		t.Error("reduced motion must disable the sheet transition")
	}
	if !regexp.MustCompile(`#sheet-close,\s*#sheet-backdrop\s*\{\s*display:none`).MatchString(css) {
		t.Error("#sheet-close and #sheet-backdrop must be hidden above 700px")
	}
	if !regexp.MustCompile(`\.room-sidebar \.timer-card\s*\{\s*display:none`).MatchString(m) {
		t.Error("the timer card must be hidden inside the sheet")
	}
}

func TestFrontendMobileKeyboard(t *testing.T) {
	b, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	sync := funcBody(t, js, "function syncViewport(")
	if !strings.Contains(sync, "lastHeight") {
		t.Error("syncViewport must only re-anchor when the height changed")
	}
	for _, want := range []string{"visualViewport", "vv.height", "vv.offsetTop", "--app-height", "removeProperty('--app-height')", "removeProperty('top')", "state.atBottom", "scrollHeight"} {
		if !strings.Contains(sync, want) {
			t.Errorf("syncViewport lacks %s", want)
		}
	}
	for _, want := range []string{"visualViewport.addEventListener('resize'", "visualViewport.addEventListener('scroll'", "globalThis.visualViewport"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	trust := funcBody(t, js, "function renderTrust(")
	for _, want := range []string{"state.composerFocus", "MOBILE.matches", "'verify', 'verified', 'later'", "document.activeElement === $('message')"} {
		if !strings.Contains(trust, want) {
			t.Errorf("renderTrust lacks %s", want)
		}
	}
	for _, ev := range []string{"focusin", "focusout"} {
		i := strings.Index(js, "$('message').addEventListener('"+ev+"'")
		if i < 0 {
			t.Fatalf("no %s handler on #message", ev)
		}
		h := js[i:]
		h = h[:strings.Index(h, "});")]
		if strings.Contains(h, "e2ee.verified =") || strings.Contains(h, "trustOpen =") {
			t.Errorf("%s handler must not change verification or trustOpen", ev)
		}
		if ev == "focusin" && !strings.Contains(h, "state.atBottom = nearBottom()") {
			t.Error("focusin must record state.atBottom before the keyboard resizes the list")
		}
	}
	if !strings.Contains(js, "$('messages').addEventListener('scroll', () => { state.atBottom = nearBottom();") {
		t.Error("the #messages scroll listener must record state.atBottom")
	}
	for _, name := range []string{"async function enter(", "const restoreFailed = () => {"} {
		if !strings.Contains(funcBody(t, js, name), "syncViewport()") {
			t.Errorf("%s lacks syncViewport()", name)
		}
	}
}

// iOS Safari zooms the page when a focused field's text is under 16px, which pushed the
// mobile chat's badge, send button and ⋯ off screen. Every phone-width field must be 16px.
func TestFrontendMobileFieldsAvoidIOSZoom(t *testing.T) {
	css, err := assets.ReadFile("style.css")
	if err != nil {
		t.Fatal(err)
	}
	m := mobileCSS(t, string(css))
	rule := regexp.MustCompile(`(?s)Fields at 16px[^{]*\{[^}]*\}`).FindString(m)
	if rule == "" {
		t.Fatal("mobile block has no 16px field rule")
	}
	for _, selector := range []string{"input", "select", ".composer textarea"} {
		if !strings.Contains(rule, selector) {
			t.Errorf("16px field rule does not cover %s", selector)
		}
	}
	if !strings.Contains(rule, "font-size:16px") {
		t.Error("field rule is not 16px")
	}
}

// On a phone the keyboard-shortcut hint wrapped onto two lines and pushed the message
// counter around; there is no Shift key there, so it is hidden and the row stays one line.
func TestFrontendMobileComposerHintStaysOneLine(t *testing.T) {
	css, err := assets.ReadFile("style.css")
	if err != nil {
		t.Fatal(err)
	}
	m := mobileCSS(t, string(css))
	if !regexp.MustCompile(`\.composer-hint>span:first-child\s*\{\s*display:none`).MatchString(m) {
		t.Error("the shortcut hint is not hidden on phones")
	}
	if !regexp.MustCompile(`\.composer-hint\s*\{[^}]*white-space:nowrap`).MatchString(m) {
		t.Error("the composer hint row can still wrap on phones")
	}
}

func TestFrontendShipsItsOwnFonts(t *testing.T) {
	css, err := assets.ReadFile("style.css")
	if err != nil {
		t.Fatal(err)
	}
	style := string(css)
	for _, family := range []string{"Instrument Sans", "JetBrains Mono"} {
		block := regexp.MustCompile(`@font-face\s*\{[^}]*font-family:\s*"` + family + `"[^}]*\}`).FindString(style)
		if block == "" {
			t.Errorf("no @font-face for %s", family)
			continue
		}
		if !strings.Contains(block, "font-display:swap") || !regexp.MustCompile(`url\(/fonts/[a-z-]+\.woff2\)`).MatchString(block) {
			t.Errorf("@font-face for %s lacks font-display:swap or a /fonts/ woff2 url", family)
		}
	}
	if !strings.Contains(style, `font-family:"Instrument Sans"`) || !strings.Contains(style, `--mono:"JetBrains Mono"`) {
		t.Error(":root lacks the Instrument Sans body face or the --mono token")
	}
	for _, m := range regexp.MustCompile(`[^\n]*font:[^;]*\bmonospace[^\n]*`).FindAllString(style, -1) {
		if !strings.Contains(m, "--mono:") {
			t.Errorf("monospace font rule outside --mono: %s", m)
		}
	}
	if strings.Contains(style, "Inter") {
		t.Error("style.css still mentions Inter")
	}
	html, err := assets.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `<link rel="preload" href="/fonts/instrument-sans-latin.woff2" as="font" type="font/woff2" crossorigin>`) {
		t.Error("index.html does not preload the Instrument Sans latin subset")
	}
	files, _, _, err := Manifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fonts/instrument-sans-latin.woff2", "fonts/jetbrains-mono-latin.woff2"} {
		if len(files[name]) != 64 {
			t.Errorf("manifest lacks %s", name)
		}
	}
}

func TestFrontendNoArrowGlyphs(t *testing.T) {
	read := func(name string) string {
		b, err := assets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	html, js, css := read("index.html"), read("app.js"), read("style.css")
	for name, text := range map[string]string{"index.html": html, "app.js": js} {
		if strings.Contains(text, "↗") {
			t.Errorf("%s still contains the arrow glyph", name)
		}
	}
	for _, want := range []string{">Create room</button>", ">End the chat for both</button>"} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	for _, want := range []string{"copy.textContent = 'Copy link';", "'Link copied'"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	if strings.Count(js, "'Copy link'") < 2 {
		t.Error("copy feedback reset must set 'Copy link' again")
	}
	if !regexp.MustCompile(`::selection\s*\{[^}]*var\(--accent\)`).MatchString(css) {
		t.Error("style.css lacks an accent ::selection rule")
	}
	if !strings.Contains(css, "caret-color:var(--accent)") {
		t.Error("style.css lacks caret-color:var(--accent)")
	}
	disabled := regexp.MustCompile(`\.send-button:disabled\s*\{[^}]*\}`).FindString(css)
	if disabled == "" {
		t.Fatal("no .send-button:disabled rule")
	}
	if m := regexp.MustCompile(`background:([^;}]*)`).FindStringSubmatch(disabled); m == nil || strings.Contains(m[1], "var(--accent)") {
		t.Errorf("disabled send background must not be the accent: %s", disabled)
	}
	if !strings.Contains(disabled, "border:1px solid var(--muted-subtle)") {
		t.Errorf("disabled send needs a --muted-subtle border: %s", disabled)
	}
	if strings.Contains(disabled, "opacity:.45") {
		t.Errorf("disabled send keeps opacity:.45: %s", disabled)
	}
	for _, gone := range []string{".flow-arrow", ".card-symbol", ".primary span"} {
		if strings.Contains(css, gone) {
			t.Errorf("style.css still styles %s", gone)
		}
	}
}

func TestFrontendSentCheckAndSameTabLinks(t *testing.T) {
	appBytes, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	app := string(appBytes)
	for _, want := range []string{`from './nav.js'`, `addEventListener('hashchange'`} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js lacks %s", want)
		}
	}
	i := strings.Index(app, `addEventListener('hashchange'`)
	if i < 0 {
		t.FailNow()
	}
	listener := app[i:]
	listener = listener[:strings.Index(listener, "\n});")]
	for _, want := range []string{`event.oldURL`, `event.newURL`, `needsReload(`, `location.reload()`} {
		if !strings.Contains(listener, want) {
			t.Errorf("hashchange listener lacks %s", want)
		}
	}
	body := funcBody(t, app, "function buildMessage")
	for _, want := range []string{`self && status === 'accepted'`, `createElementNS('http://www.w3.org/2000/svg'`, `'aria-hidden', 'true'`, `sr.className = 'sr-only'; sr.textContent = 'Sent'`, "article.dataset.key = `${message.sender_id}:${message.client_message_id}`;"} {
		if !strings.Contains(body, want) {
			t.Errorf("buildMessage lacks %s", want)
		}
	}
	if strings.Contains(body, "'aria-label'") || strings.Contains(body, "'role'") {
		t.Error("the sent check icon must be hidden from assistive tech, not labelled")
	}
	if strings.Contains(body, "dataset.group += `|${time.textContent}`") == false {
		t.Error("the sent check must not change the grouping key")
	}
}

func TestFrontendSheetNotificationsAndEnd(t *testing.T) {
	read := func(name string) string {
		b, err := assets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	html, js, css := read("index.html"), read("app.js"), read("style.css")
	if strings.Contains(html, `class="sidebar-note"`) {
		t.Error("the reload note is still in index.html")
	}
	if !strings.Contains(html, `<button id="end-button" class="danger end-button" type="button">End the chat for both</button>`) {
		t.Error("End is not a danger end-button")
	}
	rn := funcBody(t, js, "function renderNotify(")
	for _, want := range []string{"'Notifications are blocked in this browser.'", "$('notify-button').hidden ="} {
		if !strings.Contains(rn, want) {
			t.Errorf("renderNotify lacks %s", want)
		}
	}
	if strings.Contains(rn, ".disabled = denied") {
		t.Error("renderNotify still disables the button when denied")
	}
	if !regexp.MustCompile(`\.end-button\s*\{[^}]*width:100%`).MatchString(css) {
		t.Error(".end-button is not full width")
	}
	mt := regexp.MustCompile(`\.end-button\s*\{[^}]*margin-top:(\d+)px`).FindStringSubmatch(css)
	if n, _ := strconv.Atoi(firstOr(mt)); n < 24 {
		t.Error(".end-button margin-top is under 24px")
	}
	if strings.Contains(css, ".danger-link") || strings.Contains(css, ".sidebar-note") {
		t.Error("unused .danger-link / .sidebar-note CSS remains")
	}
	if regexp.MustCompile(`\.notify-panel[^{]*:disabled`).MatchString(css) {
		t.Error(".notify-panel still styles a disabled button")
	}
	if !strings.Contains(funcBody(t, js, "function finish("), "$('end-button').hidden = true") {
		t.Error("finish() no longer hides end-button")
	}
}

func firstOr(m []string) string {
	if len(m) < 2 {
		return "0"
	}
	return m[1]
}

func TestFrontendSheetParticipantsAndCounter(t *testing.T) {
	read := func(name string) string {
		b, err := assets.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	html, js, css := read("index.html"), read("app.js"), read("style.css")
	for _, want := range []string{`<h3 class="participants-title">Participants</h3>`, `<p id="participants-status" class="participants-status" role="status">`} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	for _, gone := range []string{`id="participant-count"`, `>YOUR SPACE<`, `>PARTICIPANTS<`} {
		if strings.Contains(html, gone) {
			t.Errorf("index.html still has %s", gone)
		}
	}
	rp := funcBody(t, js, "function renderParticipants(")
	if !strings.Contains(rp, "'Waiting for your contact'") || !strings.Contains(rp, "state.ended") {
		t.Error("renderParticipants does not set the waiting status only while open")
	}
	if !strings.Contains(rp, "$('participants-status').textContent !== status") {
		t.Error("renderParticipants rewrites the status even when unchanged")
	}
	if !regexp.MustCompile(`\.participants-title\s*\{[^}]*color:var\(--text\)`).MatchString(css) {
		t.Error(".participants-title is not the default text colour")
	}
	if strings.Contains(rp, ".focus(") {
		t.Error("renderParticipants moves focus")
	}
	if !strings.Contains(funcBody(t, js, "function finish("), "$('participants-status').textContent = ''") {
		t.Error("finish() does not clear the participants status")
	}
	rm := funcBody(t, js, "function renderMessages(")
	if !strings.Contains(rm, ">= 450") || !strings.Contains(rm, "$('message-count').hidden") {
		t.Error("renderMessages does not hide the counter below 450")
	}
	if !regexp.MustCompile(`\.participants-title\s*\{[^}]*font-size:14px;[^}]*font-weight:600`).MatchString(css) {
		t.Error(".participants-title is not 14px semibold")
	}
	if !regexp.MustCompile(`\.participants-status\s*\{[^}]*font-size:13px`).MatchString(css) {
		t.Error(".participants-status is not 13px")
	}
	if strings.Contains(css, ".room-sidebar.open>.eyebrow") || strings.Contains(css, "participants-heading") {
		t.Error("CSS for the removed eyebrow or old heading remains")
	}
	if !regexp.MustCompile(`\.composer-hint \.byte-count\s*\{[^}]*margin-left:auto`).MatchString(css) {
		t.Error("the byte count does not stay right-aligned when the message counter is hidden")
	}
	if !regexp.MustCompile(`#message-count\s*\{[^}]*color:var\(--muted\)`).MatchString(css) {
		t.Error("#message-count does not use the muted colour of the byte count")
	}
}

// smallFontRules returns the selector of every rule, media blocks included,
// whose font-size or font shorthand sets a size below 12px.
func smallFontRules(css string) []string {
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	size := regexp.MustCompile(`(?:^|[;\s])font(?:-size)?\s*:\s*[^;}]*?(\d+(?:\.\d+)?)px`)
	var out []string
	var walk func(s string)
	walk = func(s string) {
		for {
			open := strings.Index(s, "{")
			if open < 0 {
				return
			}
			selector := strings.Join(strings.Fields(s[:open]), " ")
			depth, end := 0, -1
			for j := open; j < len(s); j++ {
				if s[j] == '{' {
					depth++
				} else if s[j] == '}' {
					depth--
					if depth == 0 {
						end = j
						break
					}
				}
			}
			if end < 0 {
				return
			}
			body := s[open+1 : end]
			if strings.HasPrefix(selector, "@") {
				walk(body)
			} else {
				for _, m := range size.FindAllStringSubmatch(body, -1) {
					if px, err := strconv.ParseFloat(m[1], 64); err == nil && px < 12 {
						out = append(out, selector)
						break
					}
				}
			}
			s = s[end+1:]
		}
	}
	walk(css)
	return out
}

func TestFrontendNoSmallTextOutsideHome(t *testing.T) {
	css, err := assets.ReadFile("style.css")
	if err != nil {
		t.Fatal(err)
	}
	// No allowlist: nothing on any screen sets text below 12px.
	for _, selector := range smallFontRules(string(css)) {
		t.Errorf("%s sets text below 12px", selector)
	}
}

func TestSmallFontRulesHelper(t *testing.T) {
	cases := []struct {
		name, css string
		want      []string
	}{
		{"shorthand below 12px", `a{font:700 10px var(--mono)}`, []string{"a"}},
		{"nested media", `@media(max-width:700px){ .p { font-size: 9px } }`, []string{".p"}},
		{"inside a comment", `/* .x{font-size:9px} */ y{color:red}`, nil},
		{"12px", `b{font-size:12px}`, nil},
		{"14px with line height", `c{font:14px/1.2 x}`, nil},
	}
	for _, c := range cases {
		got := smallFontRules(c.css)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFrontendHomeHasNoEyebrows(t *testing.T) {
	html, err := assets.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`class="eyebrow"`, "principle-num", "card-eyebrow", "card-top"} {
		if strings.Contains(string(html), s) {
			t.Errorf("index.html still contains %s", s)
		}
	}
	js, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), "card-eyebrow") {
		t.Error("app.js still references card-eyebrow")
	}
	css, err := assets.ReadFile("style.css")
	if err != nil {
		t.Fatal(err)
	}
	values := regexp.MustCompile(`letter-spacing\s*:\s*([^;}]+)`).FindAllStringSubmatch(string(css), -1)
	if len(values) == 0 {
		t.Fatal("no letter-spacing found")
	}
	em := regexp.MustCompile(`^(-?\d*\.?\d+)em$`)
	for _, v := range values {
		m := em.FindStringSubmatch(strings.TrimSpace(v[1]))
		if m == nil {
			t.Errorf("letter-spacing %q is not an em value", v[1])
			continue
		}
		if n, _ := strconv.ParseFloat(m[1], 64); n < -0.04 {
			t.Errorf("letter-spacing %q is tighter than -0.04em", v[1])
		}
	}
	if !regexp.MustCompile(`\.primary\s*\{[^}]*justify-content:center`).MatchString(string(css)) {
		t.Error(".primary does not centre its label")
	}
}
