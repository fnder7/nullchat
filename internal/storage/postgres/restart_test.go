package postgres

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/jackc/pgx/v5/pgxpool"
	"nullchat/internal/chat"
	"nullchat/internal/chat/chattest"
	"nullchat/internal/httpapi"
)

func TestCommittedRoomSurvivesServiceRestart(t *testing.T) {
	// Keep a separate pool for the existing helper's database cleanup. Neither
	// service may reuse it, or the test would miss pool-lifetime coupling.
	cleanupPool := openTestPool(t)
	if err := ApplyMigrations(testContext, cleanupPool); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(testContext, 10*time.Second)
	defer cancel()
	open := func() *pgxpool.Pool {
		t.Helper()
		pool, err := pgxpool.NewWithConfig(ctx, cleanupPool.Config())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		if err := pool.Ping(ctx); err != nil {
			t.Fatal(err)
		}
		return pool
	}
	firstPool := open()
	first := New(firstPool, testEncryptionKey)
	t.Cleanup(first.Close)
	creator, err := first.Create(ctx, chattest.CreateInput(30))
	if err != nil {
		t.Fatal(err)
	}
	message, err := first.Send(ctx, creator.SessionToken, "one", chattest.Ciphertext(1))
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	firstPool.Close()

	second := New(open(), testEncryptionKey)
	t.Cleanup(second.Close)
	events, unsubscribe, err := second.Subscribe(ctx, creator.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	select {
	case event := <-events:
		snapshot, ok := event.Payload.(chat.SnapshotEvent)
		if event.Type != "snapshot" || !ok {
			t.Fatalf("expected snapshot, got %q", event.Type)
		}
		want := creator.Room
		want.Messages = []chat.Message{message}
		if !reflect.DeepEqual(snapshot.Room, want) || snapshot.Self != creator.Self {
			t.Fatal("committed room, identity or message changed after restart")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := second.Join(ctx, chattest.JoinInput(creator.InviteToken)); err != nil {
		t.Fatalf("unconsumed invitation did not survive restart: %v", err)
	}
}

func TestDatabaseFailureIsSanitizedByHTTP(t *testing.T) {
	// A closed pool exercises real adapter failures without needing a database.
	pool, err := pgxpool.New(testContext, "postgres://unused:private-password@127.0.0.1/unused")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	service := New(pool, testEncryptionKey)
	defer service.Close()
	handler := httpapi.New(service).Handler(http.NotFoundHandler())
	in := chattest.CreateInput(30)
	createBody, _ := json.Marshal(map[string]any{"encrypted_title": in.EncryptedTitle, "ttl_minutes": in.TTLMinutes, "key_share": in.KeyShare, "join_verifier": in.JoinVerifier})
	joinBody, _ := json.Marshal(map[string]any{"invite_token": strings.Repeat("a", 64), "join_proof": chattest.Verifier, "key_share": chattest.Share()})
	for _, tc := range []struct {
		method, path, body string
		status             int
		response           string
	}{
		{"GET", "/healthz", "", 200, `{"status":"ok"}`},
		{"GET", "/readyz", "", 503, `{"error":"database_unavailable"}`},
		{"POST", "/api/v1/rooms", string(createBody), 503, `{"error":"database_unavailable"}`},
		{"POST", "/api/v1/join", string(joinBody), 503, `{"error":"database_unavailable"}`},
		{"POST", "/api/v1/end", "", 503, `{"error":"database_unavailable"}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.status || strings.TrimSpace(response.Body.String()) != tc.response {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestServerRestartPreservesCommittedRoom(t *testing.T) {
	pool := openTestPool(t)
	if err := ApplyMigrations(testContext, pool); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "server")
	build := exec.Command("go", "build", "-o", binary, "./cmd/server")
	build.Dir = "../../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, output)
	}
	ctx, cancel := context.WithTimeout(testContext, 30*time.Second)
	defer cancel()
	start := func(databaseURL, interval string) (string, func()) {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.Addr().String()
		listener.Close()
		cmd := exec.CommandContext(ctx, binary)
		cmd.Env = append(os.Environ(), "ADDR="+addr,
			"DATABASE_URL="+databaseURL,
			"IDEMPOTENCY_ENCRYPTION_KEY="+hex.EncodeToString(testEncryptionKey[:]),
			"DB_MAX_CONNS=2", "DB_OPERATION_TIMEOUT=500ms", "EXPIRY_INTERVAL="+interval)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		stopped := false
		t.Cleanup(func() {
			if !stopped {
				_ = cmd.Process.Kill()
				<-done
			}
		})
		url := "http://" + addr
		client := &http.Client{Timeout: time.Second}
		for {
			response, err := client.Get(url + "/healthz")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					break
				}
			}
			select {
			case err := <-done:
				stopped = true
				t.Fatalf("server exited before listening: %v", err)
			case <-ctx.Done():
				t.Fatal("server never became live")
			case <-time.After(10 * time.Millisecond):
			}
		}
		return url, func() {
			t.Helper()
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				stopped = true
				if err != nil {
					t.Fatalf("server shutdown: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("server did not shut down")
			}
		}
	}

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	baseURL, stop := start(databaseURL, "20ms")
	in := chattest.CreateInput(30)
	createBody, _ := json.Marshal(map[string]any{"encrypted_title": in.EncryptedTitle, "ttl_minutes": in.TTLMinutes, "key_share": in.KeyShare, "join_verifier": in.JoinVerifier})
	request, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/v1/rooms", bytes.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var creator chat.Credentials
	err = json.NewDecoder(response.Body).Decode(&creator)
	response.Body.Close()
	if err != nil || response.StatusCode != 201 {
		t.Fatalf("create: status %d, error %v", response.StatusCode, err)
	}
	dial := func(url string) (*websocket.Conn, chat.SnapshotEvent) {
		t.Helper()
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(url, "http")+"/ws", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		if err := wsjson.Write(ctx, conn, map[string]string{"type": "authenticate", "session_token": creator.SessionToken}); err != nil {
			t.Fatal(err)
		}
		var event struct {
			Type    string             `json:"type"`
			Payload chat.SnapshotEvent `json:"payload"`
		}
		if err := wsjson.Read(ctx, conn, &event); err != nil || event.Type != "snapshot" {
			t.Fatalf("snapshot: type %q, error %v", event.Type, err)
		}
		return conn, event.Payload
	}
	conn, _ := dial(baseURL)
	messageBody := chattest.Ciphertext(1)
	if err := wsjson.Write(ctx, conn, map[string]string{"type": "send_message", "client_message_id": "one", "ciphertext": messageBody}); err != nil {
		t.Fatal(err)
	}
	var message chat.Message
	for range 2 {
		var event struct {
			Type    string       `json:"type"`
			Payload chat.Message `json:"payload"`
		}
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "ack" {
			message = event.Payload
		}
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT ciphertext FROM messages WHERE id = $1`, message.ID).Scan(&stored); err != nil || stored != messageBody {
		t.Fatalf("acknowledged message was not committed to PostgreSQL: %v", err)
	}
	// Read concurrently so the client completes the server's close handshake.
	type shutdownResult struct {
		event chat.Event
		err   error
	}
	closed := make(chan shutdownResult, 1)
	go func() {
		var event chat.Event
		if err := wsjson.Read(ctx, conn, &event); err != nil {
			closed <- shutdownResult{err: err}
			return
		}
		_, _, err := conn.Read(ctx)
		closed <- shutdownResult{event: event, err: err}
	}()
	stop()
	shutdown := <-closed
	if shutdown.event.Type != "server_shutdown" || websocket.CloseStatus(shutdown.err) != websocket.StatusGoingAway || !strings.Contains(shutdown.err.Error(), "server_shutdown") {
		t.Fatalf("shutdown must emit event and allow reconnect: event = %#v, close = %v", shutdown.event, shutdown.err)
	}
	baseURL, stop = start(databaseURL, "20ms")
	reconnected, snapshot := dial(baseURL)
	if snapshot.Room.ID != creator.Room.ID || snapshot.Self != creator.Self || len(snapshot.Room.Messages) != 1 || !reflect.DeepEqual(snapshot.Room.Messages[0], message) {
		t.Fatal("restart lost the committed snapshot")
	}
	_ = reconnected.CloseNow()
	if _, err := pool.Exec(ctx, `UPDATE rooms SET expires_at = clock_timestamp() - interval '1 second' WHERE id = $1`, creator.Room.ID); err != nil {
		t.Fatal(err)
	}
	for {
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM rooms WHERE id = $1`, creator.Room.ID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "expired" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("expiry worker did not persist closure")
		case <-time.After(10 * time.Millisecond):
		}
	}
	stop()

	// A long interval distinguishes startup recovery from the periodic tick.
	if _, err := pool.Exec(ctx, `INSERT INTO rooms (id, encrypted_title, status, expires_at) VALUES ('startup-expired', 'Expired', 'open', clock_timestamp() - interval '1 second')`); err != nil {
		t.Fatal(err)
	}
	baseURL, stop = start(databaseURL, "1h")
	response, err = http.Get(baseURL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("ready = %d", response.StatusCode)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM rooms WHERE id = 'startup-expired'`).Scan(&status); err != nil || status != "expired" {
		t.Fatalf("startup expiry = %q, %v", status, err)
	}
	stop()

	// An empty, isolated search_path proves that server startup never applies
	// migrations. The normal test schema remains intact for cleanup.
	schema := "task7_" + token()[:16]
	if _, err := pool.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(testContext, 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
	})
	unmigratedURL := databaseURL + " search_path=" + schema
	if strings.HasPrefix(databaseURL, "postgres://") || strings.HasPrefix(databaseURL, "postgresql://") {
		parsed, err := url.Parse(databaseURL)
		if err != nil {
			t.Fatal("parse test database URL")
		}
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		unmigratedURL = parsed.String()
	}
	baseURL, stop = start(unmigratedURL, "1h")
	response, err = http.Post(baseURL+"/api/v1/rooms", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 503 || strings.TrimSpace(string(body)) != `{"error":"database_unavailable"}` {
		t.Fatalf("unmigrated response = %d %s, %v", response.StatusCode, body, err)
	}
	var tables int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = $1`, schema).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("server created schema objects: %d tables, %v", tables, err)
	}
	stop()
}
