package postgres

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"nullchat/internal/chat"

	"github.com/jackc/pgx/v5"
)

func token() string {
	var b [32]byte
	// Go's crypto/rand terminates the process if secure randomness is unavailable.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func digest(value string) [32]byte { return sha256.Sum256([]byte(value)) }

func validToken(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

type idempotencyRequest struct {
	enabled     bool
	keyHash     [32]byte
	endpoint    string
	fingerprint [32]byte
}

func newIdempotencyRequest(key, endpoint string, fields ...string) idempotencyRequest {
	// A JSON string array is unambiguous even when fields contain delimiters.
	encoded, _ := json.Marshal(append([]string{endpoint}, fields...))
	return idempotencyRequest{enabled: key != "", keyHash: digest(key), endpoint: endpoint, fingerprint: sha256.Sum256(encoded)}
}

func (r idempotencyRequest) lock(ctx context.Context, tx pgx.Tx) error {
	if !r.enabled {
		return nil
	}
	// Row locks alone cannot serialize two first uses of an absent key.
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(binary.BigEndian.Uint64(r.keyHash[:8])))
	return err
}

func (s *Service) replay(ctx context.Context, tx pgx.Tx, r idempotencyRequest) (chat.Credentials, bool, error) {
	if !r.enabled {
		return chat.Credentials{}, false, nil
	}
	var endpoint string
	var fingerprint, ciphertext []byte
	var roomID *string
	var expires time.Time
	err := tx.QueryRow(ctx, `SELECT endpoint, request_fingerprint, response_ciphertext, room_id, expires_at
FROM idempotency_requests WHERE key_hash = $1 FOR UPDATE`, r.keyHash[:]).Scan(&endpoint, &fingerprint, &ciphertext, &roomID, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return chat.Credentials{}, false, nil
	}
	if err != nil {
		return chat.Credentials{}, false, chat.ErrDatabaseUnavailable
	}
	if endpoint != r.endpoint || !bytes.Equal(fingerprint, r.fingerprint[:]) {
		return chat.Credentials{}, false, chat.ErrIdempotencyConflict
	}
	if roomID == nil {
		return chat.Credentials{}, false, chat.ErrUnavailable
	}
	var status string
	var roomExpires time.Time
	err = tx.QueryRow(ctx, `SELECT status, expires_at FROM rooms WHERE id = $1 FOR SHARE`, *roomID).Scan(&status, &roomExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		return chat.Credentials{}, false, chat.ErrUnavailable
	}
	if err != nil {
		return chat.Credentials{}, false, chat.ErrDatabaseUnavailable
	}
	// now() is fixed at transaction start; clock_timestamp() observes lock waits.
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return chat.Credentials{}, false, chat.ErrDatabaseUnavailable
	}
	if status != "open" || !expires.After(now) || !roomExpires.After(now) {
		return chat.Credentials{}, false, chat.ErrUnavailable
	}
	c, err := s.decryptCredentials(ciphertext)
	if err != nil {
		return chat.Credentials{}, false, chat.ErrDatabaseUnavailable
	}
	return c, true, nil
}

func (s *Service) storeReplay(ctx context.Context, tx pgx.Tx, r idempotencyRequest, c chat.Credentials) error {
	if !r.enabled {
		return nil
	}
	ciphertext, err := s.encryptCredentials(c)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO idempotency_requests
(key_hash, endpoint, request_fingerprint, response_ciphertext, room_id, expires_at)
VALUES ($1, $2, $3, $4, $5, LEAST($6::timestamptz, now() + interval '24 hours'))`, r.keyHash[:], r.endpoint, r.fingerprint[:], ciphertext, c.Room.ID, c.Room.ExpiresAt)
	return err
}

func (s *Service) encryptCredentials(c chat.Credentials) ([]byte, error) {
	plaintext, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (s *Service) decryptCredentials(ciphertext []byte) (chat.Credentials, error) {
	n := s.aead.NonceSize()
	if len(ciphertext) < n+s.aead.Overhead() {
		return chat.Credentials{}, chat.ErrDatabaseUnavailable
	}
	plaintext, err := s.aead.Open(nil, ciphertext[:n], ciphertext[n:], nil)
	if err != nil {
		return chat.Credentials{}, err
	}
	var c chat.Credentials
	if err := json.Unmarshal(plaintext, &c); err != nil {
		return chat.Credentials{}, err
	}
	return c, nil
}
