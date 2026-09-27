package postgres

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"nullchat/internal/chat"
	"nullchat/internal/realtime"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service persists chat scenarios and coordinates local realtime subscriptions.
type Service struct {
	pool *pgxpool.Pool
	aead cipher.AEAD
	hub  *realtime.Hub
	// Acquire only after the room lock. Keep commit/publication and snapshot
	// registration/commit together so a snapshot is always the first event.
	mu sync.Mutex
}

func New(pool *pgxpool.Pool, key [32]byte) *Service {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic("invalid AES-256 key size") // Impossible with a [32]byte key.
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic("invalid AES-GCM configuration")
	}
	return &Service{pool: pool, aead: aead, hub: realtime.New()}
}

// Rollback must release locks even when the operation's context was canceled.
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func databaseNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, err
}

func (s *Service) Create(ctx context.Context, input chat.CreateInput) (chat.Credentials, error) {
	title, err := chat.NormalizeTitle(input.Title)
	if err != nil || input.TTLMinutes < 1 || input.TTLMinutes > 60 {
		return chat.Credentials{}, chat.ErrInput
	}
	r := newIdempotencyRequest(input.IdempotencyKey, "/api/v1/rooms", title, strconv.Itoa(input.TTLMinutes))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	defer rollback(tx)
	if err := r.lock(ctx, tx); err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(982463118)`); err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	if c, found, err := s.replay(ctx, tx, r); err != nil {
		return chat.Credentials{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return chat.Credentials{}, chat.ErrDatabaseUnavailable
		}
		return c, nil
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM rooms WHERE status = 'open' AND expires_at > clock_timestamp()`).Scan(&count); err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	if count >= chat.MaxRooms {
		return chat.Credentials{}, chat.ErrCapacity
	}
	roomID, invite := token(), token()
	var expires time.Time
	if err := tx.QueryRow(ctx, `INSERT INTO rooms (id, title, status, expires_at)
VALUES ($1, $2, 'open', clock_timestamp() + make_interval(mins => $3)) RETURNING expires_at`, roomID, title, input.TTLMinutes).Scan(&expires); err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	c, err := addParticipant(ctx, tx, roomID, expires, nil)
	if err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	c.InviteToken = invite
	hash := digest(invite)
	if _, err := tx.Exec(ctx, `INSERT INTO invites (token_hash, room_id, expires_at) VALUES ($1, $2, $3)`, hash[:], roomID, expires); err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	if err := s.storeReplay(ctx, tx, r, c); err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	if err := tx.Commit(ctx); err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	return c, nil
}

func (s *Service) Join(ctx context.Context, input chat.JoinInput) (chat.Credentials, error) {
	if !validToken(input.InviteToken) {
		return chat.Credentials{}, chat.ErrUnavailable
	}
	r := newIdempotencyRequest(input.IdempotencyKey, "/api/v1/join", input.InviteToken)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	defer rollback(tx)
	if err := r.lock(ctx, tx); err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	if c, found, err := s.replay(ctx, tx, r); err != nil {
		return chat.Credentials{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return chat.Credentials{}, chat.ErrDatabaseUnavailable
		}
		return c, nil
	}
	hash := digest(input.InviteToken)
	var roomID, status string
	var consumed *time.Time
	var expires, inviteExpires time.Time
	// Resolve without locking, then use the room -> invite order shared with
	// terminal operations. Re-read the invite under lock before authorizing it.
	err = tx.QueryRow(ctx, `SELECT room_id FROM invites WHERE token_hash = $1`, hash[:]).Scan(&roomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return chat.Credentials{}, chat.ErrUnavailable
	}
	if err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	err = tx.QueryRow(ctx, `SELECT status, expires_at FROM rooms WHERE id = $1 FOR UPDATE`, roomID).Scan(&status, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return chat.Credentials{}, chat.ErrUnavailable
	}
	if err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	err = tx.QueryRow(ctx, `SELECT consumed_at, expires_at FROM invites WHERE token_hash = $1 AND room_id = $2 FOR UPDATE`, hash[:], roomID).Scan(&consumed, &inviteExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		return chat.Credentials{}, chat.ErrUnavailable
	}
	if err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	if consumed != nil || status != "open" || !expires.After(now) || !inviteExpires.After(now) {
		return chat.Credentials{}, chat.ErrUnavailable
	}
	participants, err := readParticipants(ctx, tx, roomID)
	if err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	if len(participants) >= 2 {
		return chat.Credentials{}, chat.ErrRoomFull
	}
	c, err := addParticipant(ctx, tx, roomID, expires, participants)
	if err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	if _, err := tx.Exec(ctx, `UPDATE invites SET consumed_at = now() WHERE token_hash = $1`, hash[:]); err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	if err := s.storeReplay(ctx, tx, r, c); err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := tx.Commit(ctx); err != nil {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	s.hub.Publish(roomID, chat.Event{Type: "participants", Payload: c.Room.Participants})
	return c, nil
}

func addParticipant(ctx context.Context, tx pgx.Tx, roomID string, expires time.Time, existing []chat.Participant) (chat.Credentials, error) {
	name, err := chat.GenerateParticipantName(existing)
	if err != nil {
		return chat.Credentials{}, err
	}
	p := chat.Participant{ID: token(), Name: name}
	if _, err := tx.Exec(ctx, `INSERT INTO participants (id, room_id, slot, name) VALUES ($1, $2, $3, $4)`, p.ID, roomID, len(existing)+1, p.Name); err != nil {
		return chat.Credentials{}, err
	}
	secret := token()
	hash := digest(secret)
	if _, err := tx.Exec(ctx, `INSERT INTO sessions (token_hash, participant_id, room_id, expires_at) VALUES ($1, $2, $3, $4)`, hash[:], p.ID, roomID, expires); err != nil {
		return chat.Credentials{}, err
	}
	snapshot, err := readSnapshot(ctx, tx, roomID)
	if err != nil {
		return chat.Credentials{}, err
	}
	return chat.Credentials{Room: snapshot, Self: p, SessionToken: secret}, nil
}

// Subscribe holds a room share lock across snapshot capture and registration.
// Writers take the corresponding update lock before changing room state.
func (s *Service) Subscribe(ctx context.Context, secret string) (<-chan chat.Event, func(), error) {
	if !validToken(secret) {
		return nil, nil, chat.ErrSession
	}
	tx, err := s.pool.Begin(ctx) // Read-write: PostgreSQL disallows FOR SHARE in read-only transactions.
	if err != nil {
		return nil, nil, chat.ErrDatabaseUnavailable
	}
	defer rollback(tx)
	hash := digest(secret)
	var roomID string
	err = tx.QueryRow(ctx, `SELECT room_id FROM sessions WHERE token_hash = $1`, hash[:]).Scan(&roomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, chat.ErrSession
	}
	if err != nil {
		return nil, nil, chat.ErrDatabaseUnavailable
	}
	var status string
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT status, expires_at FROM rooms WHERE id = $1 FOR SHARE`, roomID).Scan(&status, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, chat.ErrSession
	}
	if err != nil {
		return nil, nil, chat.ErrDatabaseUnavailable
	}
	// Recheck authorization after taking the room lock, including time spent waiting.
	var self chat.Participant
	err = tx.QueryRow(ctx, `SELECT p.id, p.name FROM sessions s JOIN participants p ON p.room_id = s.room_id AND p.id = s.participant_id
WHERE s.token_hash = $1 AND s.room_id = $2 AND s.revoked_at IS NULL AND s.expires_at > clock_timestamp()`, hash[:], roomID).Scan(&self.ID, &self.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, chat.ErrSession
	}
	if err != nil {
		return nil, nil, chat.ErrDatabaseUnavailable
	}
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return nil, nil, chat.ErrDatabaseUnavailable
	}
	if status != "open" || !expires.After(now) {
		return nil, nil, chat.ErrSession
	}
	snapshot, err := readSnapshot(ctx, tx, roomID)
	if err != nil {
		return nil, nil, chat.ErrDatabaseUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.hub.Subscribe(roomID, hex.EncodeToString(hash[:]), 2)
	if !ok {
		return nil, nil, chat.ErrConnectionLimit
	}
	unsubscribe := func() { s.hub.Unsubscribe(sub) }
	sub.Events <- chat.Event{Type: "snapshot", Payload: chat.SnapshotEvent{Room: snapshot, Self: self}}
	if err := tx.Commit(ctx); err != nil {
		unsubscribe()
		return nil, nil, chat.ErrDatabaseUnavailable
	}
	return sub.Events, unsubscribe, nil
}

func readParticipants(ctx context.Context, tx pgx.Tx, roomID string) ([]chat.Participant, error) {
	rows, err := tx.Query(ctx, `SELECT id, name FROM participants WHERE room_id = $1 ORDER BY slot`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	participants := make([]chat.Participant, 0, 2)
	for rows.Next() {
		var p chat.Participant
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, err
		}
		participants = append(participants, p)
	}
	return participants, rows.Err()
}

// The caller must hold a room lock for the entire snapshot read.
func readSnapshot(ctx context.Context, tx pgx.Tx, roomID string) (chat.Snapshot, error) {
	r := chat.Snapshot{Messages: []chat.Message{}}
	if err := tx.QueryRow(ctx, `SELECT id, title, expires_at FROM rooms WHERE id = $1`, roomID).Scan(&r.ID, &r.Title, &r.ExpiresAt); err != nil {
		return chat.Snapshot{}, err
	}
	var err error
	r.Participants, err = readParticipants(ctx, tx, roomID)
	if err != nil {
		return chat.Snapshot{}, err
	}
	rows, err := tx.Query(ctx, `SELECT sequence, id, client_message_id, participant_id, text, created_at FROM messages WHERE room_id = $1 ORDER BY sequence`, roomID)
	if err != nil {
		return chat.Snapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var m chat.Message
		if err := rows.Scan(&m.Sequence, &m.ID, &m.ClientID, &m.SenderID, &m.Text, &m.CreatedAt); err != nil {
			return chat.Snapshot{}, err
		}
		r.Messages = append(r.Messages, m)
	}
	return r, rows.Err()
}

// Database details never cross the application boundary. A caller's canceled
// context remains distinguishable from an unavailable database.
func databaseError(ctx context.Context, _ error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return chat.ErrDatabaseUnavailable
}

type lockedSession struct {
	roomID, participantID      string
	nextSequence, messageCount int
	lastSend                   *time.Time
	now                        time.Time
}

// Resolve first, then lock room -> session, matching Join and terminal
// transitions. The unlocked lookup grants no access; authorization is checked
// again with fresh database time after both locks have been acquired.
func lockSession(ctx context.Context, tx pgx.Tx, secret string) (lockedSession, error) {
	var session lockedSession
	hash := digest(secret)
	err := tx.QueryRow(ctx, `SELECT room_id FROM sessions WHERE token_hash = $1`, hash[:]).Scan(&session.roomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, chat.ErrSession
	}
	if err != nil {
		return session, databaseError(ctx, err)
	}
	var status string
	var roomExpires, sessionExpires time.Time
	err = tx.QueryRow(ctx, `SELECT status, expires_at, next_message_sequence, message_count FROM rooms WHERE id = $1 FOR UPDATE`, session.roomID).Scan(&status, &roomExpires, &session.nextSequence, &session.messageCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, chat.ErrSession
	}
	if err != nil {
		return session, databaseError(ctx, err)
	}
	var revoked *time.Time
	err = tx.QueryRow(ctx, `SELECT participant_id, expires_at, revoked_at, last_send_at FROM sessions WHERE token_hash = $1 AND room_id = $2 FOR UPDATE`, hash[:], session.roomID).Scan(&session.participantID, &sessionExpires, &revoked, &session.lastSend)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, chat.ErrSession
	}
	if err != nil {
		return session, databaseError(ctx, err)
	}
	session.now, err = databaseNow(ctx, tx)
	if err != nil {
		return session, databaseError(ctx, err)
	}
	if revoked != nil || status != "open" || !roomExpires.After(session.now) || !sessionExpires.After(session.now) {
		return session, chat.ErrSession
	}
	return session, nil
}

func (s *Service) Send(ctx context.Context, secret, clientID, body string) (chat.Message, error) {
	if err := ctx.Err(); err != nil {
		return chat.Message{}, err
	}
	if !validToken(secret) {
		return chat.Message{}, chat.ErrSession
	}
	body, err := normalizeSendInput(clientID, body)
	if err != nil {
		return chat.Message{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return chat.Message{}, databaseError(ctx, err)
	}
	defer rollback(tx)
	session, err := lockSession(ctx, tx, secret)
	if err != nil {
		return chat.Message{}, err
	}
	var message chat.Message
	err = tx.QueryRow(ctx, `SELECT sequence, id, client_message_id, participant_id, text, created_at FROM messages WHERE room_id = $1 AND participant_id = $2 AND client_message_id = $3`, session.roomID, session.participantID, clientID).Scan(&message.Sequence, &message.ID, &message.ClientID, &message.SenderID, &message.Text, &message.CreatedAt)
	if err == nil {
		if message.Text != body {
			return chat.Message{}, chat.ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return chat.Message{}, databaseError(ctx, err)
		}
		return message, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return chat.Message{}, databaseError(ctx, err)
	}
	if session.messageCount >= chat.MaxMessages {
		return chat.Message{}, chat.ErrMessageLimit
	}
	if session.lastSend != nil && session.now.Sub(*session.lastSend) < 200*time.Millisecond {
		return chat.Message{}, chat.ErrRate
	}
	message = chat.Message{Sequence: session.nextSequence, ID: token(), ClientID: clientID, SenderID: session.participantID, Text: body, CreatedAt: session.now}
	if _, err := tx.Exec(ctx, `INSERT INTO messages (id, room_id, participant_id, client_message_id, sequence, text, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`, message.ID, session.roomID, message.SenderID, message.ClientID, message.Sequence, message.Text, message.CreatedAt); err != nil {
		return chat.Message{}, databaseError(ctx, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE rooms SET message_count = message_count + 1, next_message_sequence = next_message_sequence + 1 WHERE id = $1`, session.roomID); err != nil {
		return chat.Message{}, databaseError(ctx, err)
	}
	hash := digest(secret)
	if _, err := tx.Exec(ctx, `UPDATE sessions SET last_send_at = $2 WHERE token_hash = $1`, hash[:], session.now); err != nil {
		return chat.Message{}, databaseError(ctx, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := tx.Commit(ctx); err != nil {
		return chat.Message{}, databaseError(ctx, err)
	}
	s.hub.Publish(session.roomID, chat.Event{Type: "message", Payload: message})
	return message, nil
}

func normalizeSendInput(clientID, body string) (string, error) {
	if strings.IndexByte(clientID, 0) >= 0 || strings.IndexByte(body, 0) >= 0 {
		return "", chat.ErrInput
	}
	return chat.NormalizeMessage(clientID, body)
}

// The caller holds the room update lock. Only the transaction that changes an
// open room owns its terminal notification; retries leave timestamps intact.
func closeRoomTx(ctx context.Context, tx pgx.Tx, roomID, reason string) (bool, error) {
	var closedAt time.Time
	err := tx.QueryRow(ctx, `UPDATE rooms SET status = $2, closed_at = clock_timestamp() WHERE id = $1 AND status = 'open' RETURNING closed_at`, roomID, reason).Scan(&closedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = COALESCE(revoked_at, $2) WHERE room_id = $1`, roomID, closedAt); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE invites SET consumed_at = COALESCE(consumed_at, $2) WHERE room_id = $1`, roomID, closedAt); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) End(ctx context.Context, secret string) error {
	if !validToken(secret) {
		return chat.ErrSession
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return databaseError(ctx, err)
	}
	defer rollback(tx)
	session, err := lockSession(ctx, tx, secret)
	if err != nil {
		return err
	}
	changed, err := closeRoomTx(ctx, tx, session.roomID, "closed")
	if err != nil {
		return databaseError(ctx, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := tx.Commit(ctx); err != nil {
		return databaseError(ctx, err)
	}
	if changed {
		s.hub.CloseRoom(session.roomID, chat.Event{Type: "room_closed"})
	}
	return nil
}

func (s *Service) Expire(ctx context.Context) error {
	for {
		count, err := s.expireBatch(ctx)
		if err != nil {
			return err
		}
		if count == 0 {
			break
		}
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM idempotency_requests WHERE expires_at <= clock_timestamp()`); err != nil {
		return databaseError(ctx, err)
	}
	return nil
}

func (s *Service) expireBatch(ctx context.Context) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, databaseError(ctx, err)
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, `SELECT id FROM rooms WHERE status = 'open' AND expires_at <= clock_timestamp() ORDER BY expires_at, id LIMIT 100 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return 0, databaseError(ctx, err)
	}
	roomIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, databaseError(ctx, err)
	}
	closed := make([]string, 0, len(roomIDs))
	for _, roomID := range roomIDs {
		changed, err := closeRoomTx(ctx, tx, roomID, "expired")
		if err != nil {
			return 0, databaseError(ctx, err)
		}
		if changed {
			closed = append(closed, roomID)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := tx.Commit(ctx); err != nil {
		return 0, databaseError(ctx, err)
	}
	for _, roomID := range closed {
		s.hub.CloseRoom(roomID, chat.Event{Type: "room_expired"})
	}
	return len(roomIDs), nil
}

func (s *Service) Ready(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return databaseError(ctx, err)
	}
	return nil
}

// Close releases local subscriptions; rooms, credentials and the caller-owned
// database pool survive. Serialize with Subscribe's snapshot enqueue.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hub.CloseAll()
}

var _ chat.Application = (*Service)(nil)
