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
	"nullchat/web"
)

func TestHTTPValidationAndOrigin(t *testing.T) {
	h := New(chat.New()).Handler(web.Handler())
	for _, test := range []struct {
		name, path, body, origin string
		status                   int
	}{
		{"create", "/api/v1/rooms", `{}`, "", 201},
		{"invalid ttl", "/api/v1/rooms", `{"ttl_minutes":0}`, "", 400},
		{"unknown field", "/api/v1/rooms", `{"matrix":true}`, "", 400},
		{"trailing json", "/api/v1/rooms", `{} {}`, "", 400},
		{"large body", "/api/v1/rooms", `{"title":"` + strings.Repeat("a", 5000) + `"}`, "", 400},
		{"foreign origin", "/api/v1/rooms", `{}`, "https://evil.example", 403},
		{"opaque origin", "/api/v1/rooms", `{}`, "null", 403},
		{"same origin", "/api/v1/rooms", `{}`, "http://example.com", 201},
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
	if w.Code != 200 || !strings.Contains(w.Body.String(), "nullchat") {
		t.Fatal("embedded frontend missing")
	}
}

func TestWebSocketRoundTripReconnectAndRevocation(t *testing.T) {
	service := chat.New()
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
	creator := post("/api/v1/rooms", map[string]any{"title": "Integration", "ttl_minutes": 30})
	guest := post("/api/v1/join", map[string]string{"invite_token": creator.InviteToken})
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
	cmd := command{Type: "send_message", ClientID: "stable-id", Text: "Hello <script>alert(1)</script>"}
	if err := wsjson.Write(ctx, a, cmd); err != nil {
		t.Fatal(err)
	}
	var received chat.Message
	if err := json.Unmarshal(read(b, "message"), &received); err != nil {
		t.Fatal(err)
	}
	if received.Text != cmd.Text || received.Sequence != 1 {
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
	service := chat.New()
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
