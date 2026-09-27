package chat

import (
	"context"
	"errors"
	"time"

	"nullchat/internal/realtime"
)

var (
	ErrUnavailable         = errors.New("room_unavailable")
	ErrSession             = errors.New("session_invalid")
	ErrInput               = errors.New("invalid_input")
	ErrCapacity            = errors.New("capacity_reached")
	ErrRoomFull            = errors.New("room_full")
	ErrMessageLimit        = errors.New("message_limit")
	ErrConnectionLimit     = errors.New("connection_limit")
	ErrRate                = errors.New("rate_limited")
	ErrConflict            = errors.New("message_id_conflict")
	ErrIdempotencyConflict = errors.New("idempotency_key_conflict")
	ErrDatabaseUnavailable = errors.New("database_unavailable")
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

type CreateInput struct {
	Title          string
	TTLMinutes     int
	IdempotencyKey string
}

type JoinInput struct {
	InviteToken    string
	IdempotencyKey string
}

type Event = realtime.Event

type SnapshotEvent struct {
	Room Snapshot    `json:"room"`
	Self Participant `json:"self"`
}

// Application is the transport-independent port used by HTTP and WebSocket adapters.
// Methods represent complete scenarios, not Get/Save primitives, so an adapter can
// keep admission, deduplication and ordering atomic.
type Application interface {
	Create(context.Context, CreateInput) (Credentials, error)
	Join(context.Context, JoinInput) (Credentials, error)
	Subscribe(context.Context, string) (<-chan Event, func(), error)
	Send(context.Context, string, string, string) (Message, error)
	End(context.Context, string) error
	Ready(context.Context) error
}
