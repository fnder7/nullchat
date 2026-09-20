// Package chat owns the temporary room lifecycle. It has no HTTP or WebSocket dependencies.
package chat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrUnavailable = errors.New("room_unavailable")
	ErrSession     = errors.New("session_invalid")
	ErrInput       = errors.New("invalid_input")
	ErrCapacity    = errors.New("capacity_reached")
	ErrRate        = errors.New("rate_limited")
	ErrConflict    = errors.New("message_id_conflict")
)

const (
	MaxRooms     = 100
	MaxMessages  = 500
	MaxTextBytes = 4000
)

type Participant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Message struct {
	Sequence  int       `json:"sequence"`
	ID        string    `json:"id"`
	ClientID  string    `json:"client_message_id"`
	SenderID  string    `json:"sender_id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

type Snapshot struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	ExpiresAt    time.Time     `json:"expires_at"`
	Participants []Participant `json:"participants"`
	Messages     []Message     `json:"messages"`
}

type Credentials struct {
	Room         Snapshot    `json:"room"`
	Self         Participant `json:"self"`
	SessionToken string      `json:"session_token"`
	InviteToken  string      `json:"invite_token,omitempty"`
}

type Event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

type SnapshotEvent struct {
	Room Snapshot    `json:"room"`
	Self Participant `json:"self"`
}

type subscription struct {
	session *session
	events  chan Event
}

type room struct {
	id, title    string
	expiresAt    time.Time
	invite       [32]byte
	participants []Participant
	messages     []Message
	sessions     map[[32]byte]*session
	subs         map[*subscription]struct{}
}

type session struct {
	room        *room
	self        Participant
	lastSend    time.Time
	connections int
}

// Service is the single-process demo implementation. One mutex makes admission,
// snapshot/subscription, message ordering and invite consumption atomic.
// Network I/O never happens while holding the mutex.
type Service struct {
	mu       sync.Mutex
	now      func() time.Time
	rooms    map[string]*room
	invites  map[[32]byte]*room
	sessions map[[32]byte]*session
	closed   bool
}

var generatedNames = []string{
	"Amber", "Aspen", "Atlas", "Basil", "Birch", "Brook", "Cedar", "Clover",
	"Comet", "Coral", "Dawn", "Echo", "Ember", "Fern", "Flint", "Glade",
	"Harbor", "Hazel", "Indigo", "Iris", "Jasper", "Juniper", "Lumen", "Maple",
	"Mica", "Moss", "Nova", "Olive", "Onyx", "Orchid", "River", "Robin",
	"Sable", "Sage", "Solstice", "Stone", "Summit", "Terra", "Willow", "Zephyr",
}

func New() *Service {
	return &Service{now: time.Now, rooms: make(map[string]*room), invites: make(map[[32]byte]*room), sessions: make(map[[32]byte]*session)}
}

func token() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure random source unavailable")
	}
	return hex.EncodeToString(b[:])
}

func digest(s string) [32]byte { return sha256.Sum256([]byte(s)) }

func randomName(r *room) string {
	used := make(map[string]struct{}, len(r.participants))
	for _, participant := range r.participants {
		used[participant.Name] = struct{}{}
	}
	for range len(generatedNames) * 2 {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(generatedNames))))
		if err != nil {
			panic("secure random source unavailable")
		}
		name := generatedNames[index.Int64()]
		if _, exists := used[name]; !exists {
			return name
		}
	}
	return "Guest " + strconv.Itoa(len(r.participants)+1)
}

func (s *Service) Create(ctx context.Context, title string, ttlMinutes int) (Credentials, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Credentials{}, err
	}
	title = strings.TrimSpace(title)
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) > 60 || ttlMinutes < 1 || ttlMinutes > 60 {
		return Credentials{}, ErrInput
	}
	if title == "" {
		title = "Quiet conversation"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	if s.closed || len(s.rooms) >= MaxRooms {
		return Credentials{}, ErrCapacity
	}
	invite := token()
	r := &room{id: token()[:24], title: title, expiresAt: s.now().Add(time.Duration(ttlMinutes) * time.Minute), invite: digest(invite), sessions: make(map[[32]byte]*session), subs: make(map[*subscription]struct{}), messages: []Message{}}
	s.rooms[r.id] = r
	s.invites[r.invite] = r
	credential := s.addSession(r)
	credential.InviteToken = invite
	return credential, nil
}

func (s *Service) addSession(r *room) Credentials {
	t := token()
	p := Participant{ID: token()[:24], Name: randomName(r)}
	ss := &session{room: r, self: p}
	r.participants = append(r.participants, p)
	r.sessions[digest(t)] = ss
	s.sessions[digest(t)] = ss
	return Credentials{Room: r.snapshot(), Self: p, SessionToken: t}
}

func (s *Service) Join(ctx context.Context, invite string) (Credentials, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Credentials{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	r := s.invites[digest(invite)]
	if r == nil || s.closed {
		return Credentials{}, ErrUnavailable
	}
	delete(s.invites, r.invite)
	c := s.addSession(r)
	s.broadcast(r, Event{Type: "participants", Payload: append([]Participant(nil), r.participants...)})
	return c, nil
}

func (r *room) snapshot() Snapshot {
	return Snapshot{ID: r.id, Title: r.title, ExpiresAt: r.expiresAt, Participants: append([]Participant{}, r.participants...), Messages: append([]Message{}, r.messages...)}
}

// Subscribe atomically captures history and registers the event stream so that
// a message cannot fall into a gap between fetching history and subscribing.
func (s *Service) Subscribe(ctx context.Context, t string) (<-chan Event, func(), error) {
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
		return nil, nil, ErrSession
	}
	if ss.connections >= 2 {
		return nil, nil, ErrCapacity
	}
	sub := &subscription{session: ss, events: make(chan Event, 64)}
	ss.connections++
	ss.room.subs[sub] = struct{}{}
	sub.events <- Event{Type: "snapshot", Payload: SnapshotEvent{Room: ss.room.snapshot(), Self: ss.self}}
	return sub.events, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.removeSub(ss.room, sub)
	}, nil
}

func (s *Service) removeSub(r *room, sub *subscription) {
	if _, ok := r.subs[sub]; ok {
		delete(r.subs, sub)
		sub.session.connections--
		close(sub.events)
	}
}

func (s *Service) broadcast(r *room, event Event) {
	for sub := range r.subs {
		select {
		case sub.events <- event:
		default:
			s.removeSub(r, sub)
		}
	}
}

func (s *Service) Send(ctx context.Context, t, clientID, body string) (Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	body = strings.TrimSpace(body)
	if clientID == "" || len(clientID) > 64 || body == "" || len(body) > MaxTextBytes || !utf8.ValidString(body) {
		return Message{}, ErrInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	ss := s.sessions[digest(t)]
	if ss == nil {
		return Message{}, ErrSession
	}
	r := ss.room
	for _, m := range r.messages {
		if m.SenderID == ss.self.ID && m.ClientID == clientID {
			if m.Text != body {
				return Message{}, ErrConflict
			}
			return m, nil
		}
	}
	if len(r.messages) >= MaxMessages {
		return Message{}, ErrCapacity
	}
	now := s.now()
	if !ss.lastSend.IsZero() && now.Sub(ss.lastSend) < 200*time.Millisecond {
		return Message{}, ErrRate
	}
	ss.lastSend = now
	m := Message{Sequence: len(r.messages) + 1, ID: token()[:24], ClientID: clientID, SenderID: ss.self.ID, Text: body, CreatedAt: now}
	r.messages = append(r.messages, m)
	s.broadcast(r, Event{Type: "message", Payload: m})
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
		return ErrSession
	}
	s.endRoom(ss.room, "room_closed")
	return nil
}

func (s *Service) endRoom(r *room, reason string) {
	for sub := range r.subs {
		// Discard queued chat events; expiration must take priority.
	drain:
		for {
			select {
			case <-sub.events:
			default:
				break drain
			}
		}
		sub.events <- Event{Type: reason, Payload: struct{}{}}
		s.removeSub(r, sub)
	}
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

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, r := range s.rooms {
		s.endRoom(r, "server_shutdown")
	}
}
