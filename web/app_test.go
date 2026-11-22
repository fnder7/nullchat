package web

import (
	"os/exec"
	"regexp"
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
		".flow .flow-arrow",
		"label span",
		"input::placeholder,textarea::placeholder",
		".card-footnote",
		".principle-num",
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
	if !strings.Contains(page, `<section id="safety-panel" class="safety-panel"><span class="eyebrow">ENCRYPTION</span><p>End-to-end encrypted in your browser.</p></section>`) {
		t.Error("the sidebar encryption note is wrong")
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
