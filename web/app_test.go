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
		"api('/api/v1/join', request.body, '', request.key, { invalid_input: 'This invite is not valid. Check the link or ask for a new one.' })",
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
		"$('join-password').addEventListener('input', () => { prepared.join = null; });",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("frontend lacks prepared-request contract: %q", required)
		}
	}
}
