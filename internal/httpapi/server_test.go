package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"nullchat/internal/chat"
	"nullchat/internal/chat/chattest"
	"nullchat/internal/storage/memory"
	"nullchat/web"
)

func createBody(ttl int) string {
	in := chattest.CreateInput(ttl)
	body, _ := json.Marshal(map[string]any{"encrypted_title": in.EncryptedTitle, "ttl_minutes": in.TTLMinutes, "key_share": in.KeyShare, "join_verifier": in.JoinVerifier})
	return string(body)
}

func joinBody(invite, proof string) map[string]any {
	return map[string]any{"invite_token": invite, "join_proof": proof, "key_share": chattest.Share()}
}

func TestHTTPValidationAndOrigin(t *testing.T) {
	h := New(memory.New()).Handler(web.Handler())
	for _, test := range []struct {
		name, path, body, origin string
		status                   int
	}{
		{"create", "/api/v1/rooms", createBody(30), "", 201},
		{"plaintext title rejected", "/api/v1/rooms", `{"title":"x"}`, "", 400},
		{"missing envelope", "/api/v1/rooms", `{}`, "", 400},
		{"invalid ttl", "/api/v1/rooms", createBody(0), "", 400},
		{"unknown field", "/api/v1/rooms", `{"matrix":true}`, "", 400},
		{"trailing json", "/api/v1/rooms", createBody(30) + ` {}`, "", 400},
		{"large body", "/api/v1/rooms", `{"encrypted_title":"` + strings.Repeat("a", 5000) + `"}`, "", 400},
		{"foreign origin", "/api/v1/rooms", createBody(30), "https://evil.example", 403},
		{"opaque origin", "/api/v1/rooms", createBody(30), "null", 403},
		{"same origin", "/api/v1/rooms", createBody(30), "http://example.com", 201},
		{"bad invite", "/api/v1/join", `{"invite_token":"x"}`, "", 400},
		{"missing session", "/api/v1/end", `{}`, "", 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			r.Header.Set("Origin", test.origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("status %d, want %d: %s", w.Code, test.status, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing no-store")
			}
		})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "nullchat") || !strings.Contains(w.Body.String(), `type="module"`) {
		t.Fatal("embedded frontend missing or not loaded as a module")
	}
	for _, path := range []string{"/crypto.js", "/keystore.js"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Header().Get("Content-Type"))
		}
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	h := New(memory.New()).Handler(web.Handler())
	for _, path := range []string{"/", "/app.js", "/healthz", "/api/v1/unknown"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		csp := w.Header().Get("Content-Security-Policy")
		for _, want := range []string{"script-src 'self'", "require-trusted-types-for 'script'", "trusted-types 'none'", "frame-ancestors 'none'", "object-src 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP lacks %q", path, want)
			}
		}
		for header, want := range map[string]string{
			"Cross-Origin-Opener-Policy":   "same-origin",
			"Cross-Origin-Resource-Policy": "same-origin",
			"X-Content-Type-Options":       "nosniff",
			"Referrer-Policy":              "no-referrer",
		} {
			if got := w.Header().Get(header); got != want {
				t.Errorf("%s: %s = %q, want %q", path, header, got, want)
			}
		}
		if !strings.Contains(w.Header().Get("Permissions-Policy"), "camera=()") {
			t.Errorf("%s: Permissions-Policy missing", path)
		}
	}
}

type spyApplication struct {
	create     chat.CreateInput
	join       chat.JoinInput
	createErr  error
	joinErr    error
	readyErr   error
	readyCalls int
}

type blockingApplication struct {
	spyApplication
	subscribeStarted  chan context.Context
	subscribeDone     chan error
	subscribeCanceled chan struct{}
}

type deadlineApplication struct {
	spyApplication
	createStarted chan context.Context
}

func (a *deadlineApplication) Create(ctx context.Context, _ chat.CreateInput) (chat.Credentials, error) {
	a.createStarted <- ctx
	<-ctx.Done()
	return chat.Credentials{}, ctx.Err()
}

func (a *blockingApplication) Subscribe(ctx context.Context, _ string) (<-chan chat.Event, func(), error) {
	a.subscribeStarted <- ctx
	var err error
	select {
	case err = <-a.subscribeDone:
	case <-ctx.Done():
		err = ctx.Err()
		close(a.subscribeCanceled)
	}
	return nil, nil, err
}

func TestProtectedOperationsReceiveFiniteOperationDeadline(t *testing.T) {
	app := &deadlineApplication{createStarted: make(chan context.Context, 1)}
	api := NewWithOperationTimeout(app, 20*time.Millisecond)
	handler := api.Handler(web.Handler())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	select {
	case ctx := <-app.createStarted:
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("protected operation has no finite deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("create did not start")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("protected operation did not honor its deadline")
	}
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "database_unavailable") {
		t.Fatalf("timeout response = %d %s", response.Code, response.Body.String())
	}
}

type commandDeadlineApplication struct {
	spyApplication
	subscribeStarted chan context.Context
	sendStarted      chan context.Context
}

func (a *commandDeadlineApplication) Subscribe(ctx context.Context, _ string) (<-chan chat.Event, func(), error) {
	a.subscribeStarted <- ctx
	events := make(chan chat.Event, 1)
	events <- chat.Event{Type: "snapshot", Payload: chat.SnapshotEvent{}}
	return events, func() {}, nil
}

func (a *commandDeadlineApplication) Send(ctx context.Context, _ string, _, _ string) (chat.Message, error) {
	a.sendStarted <- ctx
	<-ctx.Done()
	return chat.Message{}, ctx.Err()
}

func TestWebSocketCommandsReceiveFiniteOperationDeadline(t *testing.T) {
	app := &commandDeadlineApplication{
		subscribeStarted: make(chan context.Context, 1),
		sendStarted:      make(chan context.Context, 1),
	}
	api := NewWithOperationTimeout(app, 20*time.Millisecond)
	server := httptest.NewServer(api.Handler(web.Handler()))
	defer server.Close()
	defer api.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, command{Type: "authenticate", SessionToken: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	var snapshot chat.Event
	if err := wsjson.Read(ctx, conn, &snapshot); err != nil || snapshot.Type != "snapshot" {
		t.Fatalf("snapshot = %#v, %v", snapshot, err)
	}
	select {
	case subscribeCtx := <-app.subscribeStarted:
		if _, ok := subscribeCtx.Deadline(); !ok {
			t.Fatal("WebSocket subscription setup has no finite deadline")
		}
		select {
		case <-subscribeCtx.Done():
		case <-ctx.Done():
			t.Fatal("subscription setup context was not released")
		}
	case <-ctx.Done():
		t.Fatal("Subscribe did not start")
	}
	if err := wsjson.Write(ctx, conn, command{Type: "send_message", ClientID: "one", Ciphertext: chattest.Ciphertext(1)}); err != nil {
		t.Fatal(err)
	}
	select {
	case sendCtx := <-app.sendStarted:
		if _, ok := sendCtx.Deadline(); !ok {
			t.Fatal("WebSocket command has no finite deadline")
		}
	case <-ctx.Done():
		t.Fatal("Send did not start")
	}
	var response chat.Event
	if err := wsjson.Read(ctx, conn, &response); err != nil || response.Type != "error" {
		t.Fatalf("command response = %#v, %v", response, err)
	}
	if response.Payload.(map[string]any)["code"] != chat.ErrDatabaseUnavailable.Error() {
		t.Fatalf("command error = %#v", response.Payload)
	}
}

func TestShutdownCancelsBlockedSubscribeAndWaitsForHandler(t *testing.T) {
	app := &blockingApplication{
		subscribeStarted:  make(chan context.Context, 1),
		subscribeDone:     make(chan error, 1),
		subscribeCanceled: make(chan struct{}),
	}
	api := NewWithOperationTimeout(app, time.Minute)
	server := httptest.NewServer(api.Handler(web.Handler()))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, command{Type: "authenticate", SessionToken: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	select {
	case subscribeCtx := <-app.subscribeStarted:
		if _, hasDeadline := subscribeCtx.Deadline(); !hasDeadline {
			t.Fatal("subscription setup has no finite operation deadline")
		}
	case <-ctx.Done():
		t.Fatal("Subscribe did not start")
	}
	shutdownEvent := make(chan chat.Event, 1)
	closed := make(chan error, 1)
	go func() {
		var event chat.Event
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			closed <- err
			return
		}
		shutdownEvent <- event
		_, _, err := conn.Read(ctx)
		closed <- err
	}()
	shutdownDone := make(chan struct{})
	go func() {
		api.Shutdown()
		close(shutdownDone)
	}()
	select {
	case <-app.subscribeCanceled:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not wait for blocked handler cancellation")
	}
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not wait for the handler")
	}
	select {
	case event := <-shutdownEvent:
		if event.Type != "server_shutdown" {
			t.Fatalf("shutdown event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not emit server_shutdown event")
	}
	if err := <-closed; websocket.CloseStatus(err) != websocket.StatusGoingAway || !strings.Contains(err.Error(), "server_shutdown") {
		t.Fatalf("shutdown close = %v", err)
	}
}

type connectedApplication struct{ spyApplication }

func (*connectedApplication) Subscribe(context.Context, string) (<-chan chat.Event, func(), error) {
	events := make(chan chat.Event, 1)
	events <- chat.Event{Type: "snapshot", Payload: chat.SnapshotEvent{}}
	return events, func() {}, nil
}

func TestWebSocketGlobalLimitBeforeUpgradeUsesConnectionLimit(t *testing.T) {
	api := New(&connectedApplication{})
	api.connectionLimit = 1
	api.connections[new(websocket.Conn)] = struct{}{}
	server := httptest.NewServer(api.Handler(web.Handler()))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if conn != nil {
		conn.CloseNow()
	}
	if err == nil || response == nil {
		t.Fatalf("dial error = %v, response = %#v", err, response)
	}
	defer response.Body.Close()
	var payload map[string]string
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusTooManyRequests || payload["error"] != "connection_limit" {
		t.Fatalf("limit response = %d %#v", response.StatusCode, payload)
	}
}

func TestWebSocketGlobalLimitRaceAfterUpgradeUsesConnectionLimit(t *testing.T) {
	api := New(&connectedApplication{})
	api.connectionLimit = 1
	accepted := make(chan struct{}, 2)
	release := make(chan struct{})
	realAccept := api.accept
	api.accept = func(w http.ResponseWriter, r *http.Request, options *websocket.AcceptOptions) (*websocket.Conn, error) {
		conn, err := realAccept(w, r, options)
		if err == nil {
			accepted <- struct{}{}
			<-release
		}
		return conn, err
	}
	server := httptest.NewServer(api.Handler(web.Handler()))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clients := make(chan *websocket.Conn, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
			if err != nil {
				errs <- err
				return
			}
			clients <- conn
		}()
	}
	for range 2 {
		select {
		case <-accepted:
		case err := <-errs:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal("both upgrades did not reach the registration race")
		}
	}
	close(release)

	connections := make([]*websocket.Conn, 0, 2)
	for range 2 {
		select {
		case conn := <-clients:
			connections = append(connections, conn)
		case err := <-errs:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal("dial did not complete")
		}
	}
	defer func() {
		for _, conn := range connections {
			conn.CloseNow()
		}
		api.Shutdown()
	}()
	for _, conn := range connections {
		if err := wsjson.Write(ctx, conn, command{Type: "authenticate", SessionToken: strings.Repeat("a", 64)}); err != nil {
			t.Fatal(err)
		}
	}

	var snapshots, limited int
	for _, conn := range connections {
		var event struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			t.Fatal(err)
		}
		switch event.Type {
		case "snapshot":
			snapshots++
		case "error":
			var payload map[string]string
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["code"] != "connection_limit" {
				t.Fatalf("unexpected error event = %#v", payload)
			}
			limited++
			_, _, err := conn.Read(ctx)
			if websocket.CloseStatus(err) != websocket.StatusTryAgainLater {
				t.Fatalf("limit close status = %v, want 1013", websocket.CloseStatus(err))
			}
		default:
			t.Fatalf("unexpected event = %#v", event)
		}
	}
	if snapshots != 1 || limited != 1 {
		t.Fatalf("snapshots = %d, limited = %d", snapshots, limited)
	}
}

type unavailableApplication struct{ spyApplication }

func (a *unavailableApplication) Subscribe(context.Context, string) (<-chan chat.Event, func(), error) {
	return nil, nil, chat.ErrDatabaseUnavailable
}

func TestDatabaseUnavailableSubscriptionIsRetryable(t *testing.T) {
	api := New(&unavailableApplication{})
	server := httptest.NewServer(api.Handler(web.Handler()))
	defer server.Close()
	defer api.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, command{Type: "authenticate", SessionToken: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Type    string            `json:"type"`
		Payload map[string]string `json:"payload"`
	}
	if err := wsjson.Read(ctx, conn, &event); err != nil || event.Payload["code"] != "database_unavailable" {
		t.Fatalf("subscription error = %#v, %v", event, err)
	}
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusTryAgainLater {
		t.Fatalf("close status = %v, want retryable 1013", websocket.CloseStatus(err))
	}
}

func (s *spyApplication) Create(_ context.Context, input chat.CreateInput) (chat.Credentials, error) {
	s.create = input
	return chat.Credentials{}, s.createErr
}

func (s *spyApplication) Join(_ context.Context, input chat.JoinInput) (chat.Credentials, error) {
	s.join = input
	return chat.Credentials{}, s.joinErr
}

func (s *spyApplication) Subscribe(context.Context, string) (<-chan chat.Event, func(), error) {
	return nil, nil, chat.ErrSession
}

func (s *spyApplication) Send(context.Context, string, string, string) (chat.Message, error) {
	return chat.Message{}, chat.ErrSession
}

func (s *spyApplication) End(context.Context, string) error { return chat.ErrSession }

func (s *spyApplication) Ready(context.Context) error {
	s.readyCalls++
	return s.readyErr
}

func TestReadyAndDatabaseUnavailable(t *testing.T) {
	app := &spyApplication{readyErr: chat.ErrDatabaseUnavailable}
	h := New(app).Handler(web.Handler())

	ready := httptest.NewRecorder()
	h.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable || !strings.Contains(ready.Body.String(), "database_unavailable") {
		t.Fatalf("ready = %d: %s", ready.Code, ready.Body.String())
	}

	health := httptest.NewRecorder()
	h.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"status":"ok"`) {
		t.Fatalf("health = %d: %s", health.Code, health.Body.String())
	}
	if app.readyCalls != 1 {
		t.Fatalf("liveness queried the database: Ready called %d times", app.readyCalls)
	}
	app.readyErr = nil
	recovered := httptest.NewRecorder()
	h.ServeHTTP(recovered, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recovered.Code != http.StatusOK || strings.TrimSpace(recovered.Body.String()) != `{"status":"ready"}` {
		t.Fatalf("recovered readiness = %d: %s", recovered.Code, recovered.Body.String())
	}
}

func TestIdempotencyKeyForwardingAndValidation(t *testing.T) {
	const key = "12345678901234567890123456789012"
	app := &spyApplication{}
	h := New(app).Handler(web.Handler())

	create := httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader(`{}`))
	create.Header.Set("Idempotency-Key", key)
	createResponse := httptest.NewRecorder()
	h.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated || app.create.IdempotencyKey != key {
		t.Fatalf("create = %d, key = %q", createResponse.Code, app.create.IdempotencyKey)
	}

	join := httptest.NewRequest(http.MethodPost, "/api/v1/join", strings.NewReader(`{"invite_token":"`+strings.Repeat("a", 64)+`"}`))
	join.Header.Set("Idempotency-Key", key)
	joinResponse := httptest.NewRecorder()
	h.ServeHTTP(joinResponse, join)
	if joinResponse.Code != http.StatusOK || app.join.IdempotencyKey != key {
		t.Fatalf("join = %d, key = %q", joinResponse.Code, app.join.IdempotencyKey)
	}

	for _, test := range []struct {
		name, key string
		path      string
		body      string
	}{
		{"31 bytes", strings.Repeat("a", 31), "/api/v1/rooms", `{}`},
		{"129 bytes", strings.Repeat("a", 129), "/api/v1/rooms", `{}`},
		{"non-ASCII", strings.Repeat("a", 31) + "é", "/api/v1/join", `{"invite_token":"` + strings.Repeat("a", 64) + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			beforeCreate, beforeJoin := app.create, app.join
			r := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			r.Header.Set("Idempotency-Key", test.key)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_input") {
				t.Fatalf("response = %d: %s", w.Code, w.Body.String())
			}
			if app.create != beforeCreate || app.join != beforeJoin {
				t.Fatal("application called for invalid idempotency key")
			}
		})
	}
}

func TestHTTPTransportErrorMappings(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"room full", chat.ErrRoomFull, http.StatusTooManyRequests, "room_full"},
		{"message limit", chat.ErrMessageLimit, http.StatusTooManyRequests, "message_limit"},
		{"connection limit", chat.ErrConnectionLimit, http.StatusTooManyRequests, "connection_limit"},
		{"idempotency conflict", chat.ErrIdempotencyConflict, http.StatusConflict, "idempotency_key_conflict"},
		{"database unavailable", chat.ErrDatabaseUnavailable, http.StatusServiceUnavailable, "database_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := New(&spyApplication{createErr: test.err}).Handler(web.Handler())
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader(`{}`)))
			if w.Code != test.status || !strings.Contains(w.Body.String(), test.code) {
				t.Fatalf("response = %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestWebSocketRoundTripReconnectAndRevocation(t *testing.T) {
	service := memory.New()
	api := New(service)
	server := httptest.NewServer(api.Handler(web.Handler()))
	t.Cleanup(func() { service.Close(); api.Shutdown(); server.Close() })
	post := func(path string, data any) chat.Credentials {
		t.Helper()
		body, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Post(server.URL+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode >= 300 {
			t.Fatalf("POST %s: %d", path, response.StatusCode)
		}
		var c chat.Credentials
		if err := json.NewDecoder(response.Body).Decode(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	creator := post("/api/v1/rooms", json.RawMessage(createBody(30)))
	wrongBody, _ := json.Marshal(joinBody(creator.InviteToken, chattest.Blob(chat.MACBytes)))
	wrong, err := http.Post(server.URL+"/api/v1/join", "application/json", bytes.NewReader(wrongBody))
	if err != nil {
		t.Fatal(err)
	}
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong proof status %d, want 403", wrong.StatusCode)
	}
	// The failed proof did not consume the seat.
	guest := post("/api/v1/join", joinBody(creator.InviteToken, chattest.Verifier))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func(token string) *websocket.Conn {
		t.Helper()
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.CloseNow() })
		if err := wsjson.Write(ctx, c, command{Type: "authenticate", SessionToken: token}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	type wireEvent struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	read := func(c *websocket.Conn, want string) json.RawMessage {
		t.Helper()
		var e wireEvent
		if err := wsjson.Read(ctx, c, &e); err != nil {
			t.Fatal(err)
		}
		if e.Type != want {
			t.Fatalf("event %s, want %s", e.Type, want)
		}
		return e.Payload
	}
	a, b := dial(creator.SessionToken), dial(guest.SessionToken)
	read(a, "snapshot")
	read(b, "snapshot")
	body := chattest.Ciphertext(1)
	cmd := command{Type: "send_message", ClientID: "stable-id", Ciphertext: body}
	if err := wsjson.Write(ctx, a, cmd); err != nil {
		t.Fatal(err)
	}
	var received chat.Message
	if err := json.Unmarshal(read(b, "message"), &received); err != nil {
		t.Fatal(err)
	}
	if received.Ciphertext != body || received.Sequence != 1 {
		t.Fatal("message corrupted")
	}
	// ack and broadcast can arrive in either order; they refer to the same message.
	seen := map[string]bool{}
	for range 2 {
		var e wireEvent
		if err := wsjson.Read(ctx, a, &e); err != nil {
			t.Fatal(err)
		}
		seen[e.Type] = true
	}
	if !seen["ack"] || !seen["message"] {
		t.Fatal("missing acknowledgement or echo")
	}
	if err := wsjson.Write(ctx, a, cmd); err != nil {
		t.Fatal(err)
	}
	read(a, "ack")
	_ = b.CloseNow()
	b = dial(guest.SessionToken)
	var snapshot chat.SnapshotEvent
	if err := json.Unmarshal(read(b, "snapshot"), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Room.Messages) != 1 || snapshot.Room.Messages[0].ID != received.ID {
		t.Fatal("history lost or duplicate stored")
	}
	req, _ := http.NewRequest("POST", server.URL+"/api/v1/end", nil)
	req.Header.Set("Authorization", "Bearer "+guest.SessionToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("end rejected")
	}
	read(a, "room_closed")
	read(b, "room_closed")
	invalid := dial(creator.SessionToken)
	read(invalid, "error")
}

func TestWebSocketRejectsForeignOrigin(t *testing.T) {
	service := memory.New()
	api := New(service)
	server := httptest.NewServer(api.Handler(web.Handler()))
	defer server.Close()
	defer api.Shutdown()
	defer service.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}}})
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("foreign origin accepted")
	}
	if response == nil || response.StatusCode != 403 {
		t.Fatal("expected origin rejection")
	}
}
