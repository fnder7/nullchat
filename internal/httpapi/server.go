package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"nullchat/internal/chat"
)

// Chat keeps the transport dependency named locally while using the application port.
type Chat = chat.Application

type Server struct {
	chat        Chat
	mu          sync.Mutex
	connections map[*websocket.Conn]struct{}
	closing     bool
	// Global admission budget bounds unauthenticated work without retaining IPs.
	budget int
	window time.Time
}

func New(service Chat) *Server {
	return &Server{chat: service, connections: make(map[*websocket.Conn]struct{}), budget: 120, window: time.Now()}
}

func (s *Server) Handler(frontend http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/v1/rooms", s.create)
	mux.HandleFunc("POST /api/v1/join", s.join)
	mux.HandleFunc("POST /api/v1/end", s.end)
	mux.HandleFunc("GET /ws", s.socket)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { respond(w, 404, map[string]string{"error": "not_found"}) })
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			respond(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
			return
		}
		frontend.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if !sameOrigin(r) {
			respond(w, 403, map[string]string{"error": "origin_forbidden"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host == r.Host && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == ""
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, chat.ErrUnavailable):
		status = http.StatusGone
	case errors.Is(err, chat.ErrSession):
		status = http.StatusUnauthorized
	case errors.Is(err, chat.ErrCapacity), errors.Is(err, chat.ErrRate):
		status = http.StatusTooManyRequests
	case errors.Is(err, chat.ErrConflict):
		status = http.StatusConflict
	}
	respond(w, status, map[string]string{"error": err.Error()})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, chat.ErrInput)
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		fail(w, chat.ErrInput)
		return false
	}
	return true
}

func (s *Server) admit(w http.ResponseWriter) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		respond(w, 503, map[string]string{"error": "server_shutdown"})
		return false
	}
	if time.Since(s.window) >= time.Minute {
		s.window = time.Now()
		s.budget = 120
	}
	if s.budget == 0 {
		w.Header().Set("Retry-After", "60")
		fail(w, chat.ErrRate)
		return false
	}
	s.budget--
	return true
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	if !s.admit(w) {
		return
	}
	input := struct {
		Title string `json:"title"`
		TTL   int    `json:"ttl_minutes"`
	}{TTL: 30}
	if !decode(w, r, &input) {
		return
	}
	c, err := s.chat.Create(r.Context(), input.Title, input.TTL)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusCreated, c)
}

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	if !s.admit(w) {
		return
	}
	var input struct {
		Invite string `json:"invite_token"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.Invite) != 64 {
		fail(w, chat.ErrInput)
		return
	}
	c, err := s.chat.Join(r.Context(), input.Invite)
	if err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, c)
}

func (s *Server) end(w http.ResponseWriter, r *http.Request) {
	const prefix = "Bearer "
	if !strings.HasPrefix(r.Header.Get("Authorization"), prefix) {
		fail(w, chat.ErrSession)
		return
	}
	if err := s.chat.End(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), prefix)); err != nil {
		fail(w, err)
		return
	}
	respond(w, http.StatusOK, map[string]bool{"ok": true})
}

type command struct {
	Type         string `json:"type"`
	SessionToken string `json:"session_token,omitempty"`
	ClientID     string `json:"client_message_id,omitempty"`
	Text         string `json:"text,omitempty"`
}

func write(ctx context.Context, c *websocket.Conn, event chat.Event) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return wsjson.Write(ctx, c, event)
}

func (s *Server) socket(w http.ResponseWriter, r *http.Request) {
	if !s.admit(w) {
		return
	}
	s.mu.Lock()
	full := len(s.connections) >= 400 || s.closing
	s.mu.Unlock()
	if full {
		fail(w, chat.ErrCapacity)
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	if s.closing || len(s.connections) >= 400 {
		s.mu.Unlock()
		_ = c.CloseNow()
		return
	}
	s.connections[c] = struct{}{}
	s.mu.Unlock()
	defer func() { _ = c.CloseNow(); s.mu.Lock(); delete(s.connections, c); s.mu.Unlock() }()
	c.SetReadLimit(8192)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	authCtx, authCancel := context.WithTimeout(ctx, 5*time.Second)
	var auth command
	err = wsjson.Read(authCtx, c, &auth)
	authCancel()
	if err != nil || auth.Type != "authenticate" || len(auth.SessionToken) != 64 {
		_ = c.Close(websocket.StatusPolicyViolation, "authentication_required")
		return
	}
	events, unsubscribe, err := s.chat.Subscribe(ctx, auth.SessionToken)
	if err != nil {
		_ = write(ctx, c, chat.Event{Type: "error", Payload: map[string]string{"code": err.Error()}})
		_ = c.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	defer unsubscribe()
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer cancel()
		for {
			var cmd command
			if wsjson.Read(ctx, c, &cmd) != nil {
				return
			}
			if cmd.Type != "send_message" {
				_ = c.Close(websocket.StatusPolicyViolation, "unknown_command")
				return
			}
			m, err := s.chat.Send(ctx, auth.SessionToken, cmd.ClientID, cmd.Text)
			event := chat.Event{Type: "ack", Payload: m}
			if err != nil {
				event = chat.Event{Type: "error", Payload: map[string]string{"code": err.Error(), "client_message_id": cmd.ClientID}}
			}
			if write(ctx, c, event) != nil {
				return
			}
		}
	}()
	defer func() { cancel(); _ = c.CloseNow(); <-readerDone }()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				_ = c.Close(websocket.StatusTryAgainLater, "reconnect")
				return
			}
			if write(ctx, c, event) != nil {
				return
			}
			if event.Type == "room_expired" || event.Type == "room_closed" {
				_ = c.Close(websocket.StatusNormalClosure, event.Type)
				return
			}
			if event.Type == "server_shutdown" {
				_ = c.Close(websocket.StatusGoingAway, "server_shutdown")
				return
			}
		case <-ticker.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.Ping(pingCtx)
			pingCancel()
			if err != nil {
				return
			}
		}
	}
}

// Shutdown explicitly closes upgraded connections, which http.Server.Shutdown does not own.
func (s *Server) Shutdown() {
	s.mu.Lock()
	s.closing = true
	connections := make([]*websocket.Conn, 0, len(s.connections))
	for c := range s.connections {
		connections = append(connections, c)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range connections {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.Close(websocket.StatusGoingAway, "server_shutdown") }()
	}
	wg.Wait()
}
