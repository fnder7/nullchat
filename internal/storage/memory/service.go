// Package memory provides the single-process in-memory chat application.
package memory

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"

	"nullchat/internal/chat"
	"nullchat/internal/realtime"
)

type room struct {
	id, encTitle string
	expiresAt    time.Time
	invite       [32]byte
	verifier     [32]byte // SHA-256 of the join verifier; the server cannot derive it
	failedJoins  int
	participants []chat.Participant
	messages     []chat.Message
	sessions     map[[32]byte]*session
}

type session struct {
	room     *room
	self     chat.Participant
	lastSend time.Time
}

// Service is the in-memory implementation of chat.Application. One mutex makes
// admission, snapshot/subscription, message ordering and invite consumption atomic.
// Network I/O never happens while holding the mutex.
type Service struct {
	mu       sync.Mutex
	now      func() time.Time
	rooms    map[string]*room
	invites  map[[32]byte]*room
	sessions map[[32]byte]*session
	hub      *realtime.Hub
	closed   bool
}

func New() *Service {
	return &Service{now: time.Now, rooms: make(map[string]*room), invites: make(map[[32]byte]*room), sessions: make(map[[32]byte]*session), hub: realtime.New()}
}

func token() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure random source unavailable")
	}
	return hex.EncodeToString(b[:])
}

func digest(s string) [32]byte { return sha256.Sum256([]byte(s)) }

func digestKey(s string) string {
	d := digest(s)
	return hex.EncodeToString(d[:])
}

func (s *Service) Create(ctx context.Context, input chat.CreateInput) (chat.Credentials, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return chat.Credentials{}, err
	}
	if err := chat.ValidateCreate(input); err != nil {
		return chat.Credentials{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	if s.closed || len(s.rooms) >= chat.MaxRooms {
		return chat.Credentials{}, chat.ErrCapacity
	}
	invite := token()
	r := &room{id: token()[:24], encTitle: input.EncryptedTitle, expiresAt: s.now().Add(time.Duration(input.TTLMinutes) * time.Minute), invite: digest(invite), verifier: digest(input.JoinVerifier), sessions: make(map[[32]byte]*session), messages: []chat.Message{}}
	credential, err := s.addSession(r, chat.RoleCreator, input.KeyShare)
	if err != nil {
		return chat.Credentials{}, err
	}
	s.rooms[r.id] = r
	s.invites[r.invite] = r
	credential.InviteToken = invite
	return credential, nil
}

func (s *Service) addSession(r *room, role string, share chat.KeyShare) (chat.Credentials, error) {
	t := token()
	name, err := chat.GenerateParticipantName(r.participants)
	if err != nil {
		return chat.Credentials{}, err
	}
	p := chat.Participant{ID: token()[:24], Name: name, Role: role, KeyShare: share}
	ss := &session{room: r, self: p}
	r.participants = append(r.participants, p)
	r.sessions[digest(t)] = ss
	s.sessions[digest(t)] = ss
	return chat.Credentials{Room: r.snapshot(), Self: p, SessionToken: t}, nil
}

func (s *Service) Join(ctx context.Context, input chat.JoinInput) (chat.Credentials, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return chat.Credentials{}, err
	}
	if err := chat.ValidateJoin(input); err != nil {
		return chat.Credentials{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	r := s.invites[digest(input.InviteToken)]
	if r == nil || s.closed {
		return chat.Credentials{}, chat.ErrUnavailable
	}
	proof := digest(input.JoinProof)
	if subtle.ConstantTimeCompare(proof[:], r.verifier[:]) != 1 {
		// A wrong proof (password) must not consume the seat, but online guessing is bounded.
		r.failedJoins++
		if r.failedJoins >= chat.MaxJoinAttempts {
			delete(s.invites, r.invite)
			s.broadcast(r, realtime.Event{Type: "invite_revoked", Payload: struct{}{}})
		}
		return chat.Credentials{}, chat.ErrJoinProof
	}
	c, err := s.addSession(r, chat.RoleGuest, input.KeyShare)
	if err != nil {
		return chat.Credentials{}, err
	}
	delete(s.invites, r.invite)
	s.broadcast(r, realtime.Event{Type: "participants", Payload: append([]chat.Participant(nil), r.participants...)})
	return c, nil
}

func (r *room) snapshot() chat.Snapshot {
	return chat.Snapshot{ID: r.id, EncryptedTitle: r.encTitle, ExpiresAt: r.expiresAt, Participants: append([]chat.Participant{}, r.participants...), Messages: append([]chat.Message{}, r.messages...)}
}

// Subscribe atomically captures history and registers the event stream so that
// a message cannot fall into a gap between fetching history and subscribing.
func (s *Service) Subscribe(ctx context.Context, t string) (<-chan chat.Event, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	ss := s.sessions[digest(t)]
	if ss == nil || s.closed {
		return nil, nil, chat.ErrSession
	}
	sub, ok := s.hub.Subscribe(ss.room.id, digestKey(t), 2)
	if !ok {
		return nil, nil, chat.ErrCapacity
	}
	sub.Events <- realtime.Event{Type: "snapshot", Payload: chat.SnapshotEvent{Room: ss.room.snapshot(), Self: ss.self}}
	return sub.Events, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.hub.Unsubscribe(sub)
	}, nil
}

func (s *Service) broadcast(r *room, event realtime.Event) {
	s.hub.Publish(r.id, event)
}

func (s *Service) Send(ctx context.Context, t, clientID, body string) (chat.Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return chat.Message{}, err
	}
	if err := chat.ValidateMessage(clientID, body); err != nil {
		return chat.Message{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	ss := s.sessions[digest(t)]
	if ss == nil {
		return chat.Message{}, chat.ErrSession
	}
	r := ss.room
	for _, m := range r.messages {
		if m.SenderID == ss.self.ID && m.ClientID == clientID {
			if m.Ciphertext != body {
				return chat.Message{}, chat.ErrConflict
			}
			return m, nil
		}
	}
	if len(r.messages) >= chat.MaxMessages {
		return chat.Message{}, chat.ErrMessageLimit
	}
	now := s.now()
	if !ss.lastSend.IsZero() && now.Sub(ss.lastSend) < 200*time.Millisecond {
		return chat.Message{}, chat.ErrRate
	}
	ss.lastSend = now
	m := chat.Message{Sequence: len(r.messages) + 1, ID: token()[:24], ClientID: clientID, SenderID: ss.self.ID, Ciphertext: body, CreatedAt: now}
	r.messages = append(r.messages, m)
	s.broadcast(r, realtime.Event{Type: "message", Payload: m})
	return m, nil
}

// End closes the room for both participants, not just the calling connection.
func (s *Service) End(ctx context.Context, t string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	ss := s.sessions[digest(t)]
	if ss == nil {
		return chat.ErrSession
	}
	s.endRoom(ss.room, "room_closed")
	return nil
}

func (s *Service) endRoom(r *room, reason string) {
	// Discard queued chat events; expiration must take priority.
	s.hub.CloseRoom(r.id, realtime.Event{Type: reason, Payload: struct{}{}})
	for key := range r.sessions {
		delete(s.sessions, key)
	}
	delete(s.invites, r.invite)
	delete(s.rooms, r.id)
	// No promise of secure memory erasure: just release retained application data.
	r.messages = nil
}

func (s *Service) expireLocked() {
	for _, r := range s.rooms {
		if !s.now().Before(r.expiresAt) {
			s.endRoom(r, "room_expired")
		}
	}
}

func (s *Service) Expire() { s.mu.Lock(); defer s.mu.Unlock(); s.expireLocked() }

func (s *Service) Ready(context.Context) error { return nil }

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, r := range s.rooms {
		s.endRoom(r, "server_shutdown")
	}
}

var _ chat.Application = (*Service)(nil)
