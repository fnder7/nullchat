package postgres

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nullchat/internal/chat"
	"nullchat/internal/chat/chattest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testEncryptionKey = [32]byte{1, 2, 3, 4}

func newTestService(t *testing.T) *Service {
	t.Helper()
	pool := openTestPool(t)
	if err := ApplyMigrations(testContext, pool); err != nil {
		t.Fatal(err)
	}
	return New(pool, testEncryptionKey)
}

func newCreatedService(t *testing.T) (*Service, chat.Credentials) {
	t.Helper()
	s := newTestService(t)
	return s, createRoom(t, s, "")
}

func createRoom(t *testing.T, s *Service, key string) chat.Credentials {
	t.Helper()
	c, err := s.Create(testContext, createInput(key))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Encrypted fields are random, but a retry with the same Idempotency-Key must
// resend the identical body (as the browser does). These helpers therefore
// return one stable request per key, so tests can say "the same request" by
// calling them again.
var (
	requestsMu     sync.Mutex
	createRequests = map[string]chat.CreateInput{}
	joinRequests   = map[[2]string]chat.JoinInput{}
)

func createInput(key string) chat.CreateInput {
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if in, ok := createRequests[key]; ok && key != "" {
		return in
	}
	in := chattest.CreateInput(30)
	in.IdempotencyKey = key
	createRequests[key] = in
	return in
}

func joinInput(invite, key string) chat.JoinInput {
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if in, ok := joinRequests[[2]string{invite, key}]; ok && key != "" {
		return in
	}
	in := chattest.JoinInput(invite)
	in.IdempotencyKey = key
	joinRequests[[2]string{invite, key}] = in
	return in
}

func assertSameCredentials(t *testing.T, got, want chat.Credentials) {
	t.Helper()
	a, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("credentials differ from original response")
	}
}

func TestJoinConsumesInviteAtomically(t *testing.T) {
	s, creator := newCreatedService(t)
	var success atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		wg.Go(func() {
			<-start
			if _, err := s.Join(testContext, chattest.JoinInput(creator.InviteToken)); err == nil {
				success.Add(1)
			} else if !errors.Is(err, chat.ErrUnavailable) {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	if success.Load() != 1 {
		t.Fatalf("joined = %d", success.Load())
	}
	var participants, sessions int
	if err := s.pool.QueryRow(testContext, `SELECT (SELECT count(*) FROM participants), (SELECT count(*) FROM sessions)`).Scan(&participants, &sessions); err != nil {
		t.Fatal(err)
	}
	if participants != 2 || sessions != 2 {
		t.Fatalf("participants = %d, sessions = %d", participants, sessions)
	}
}

func TestCreateIdempotencyFingerprintConflict(t *testing.T) {
	s := newTestService(t)
	key := strings.Repeat("a", 32)
	original := createInput(key)
	first, err := s.Create(testContext, original)
	if err != nil {
		t.Fatal(err)
	}
	// A retry must resend the identical encrypted body; it gets the same room.
	replay, err := New(s.pool, testEncryptionKey).Create(testContext, original)
	if err != nil {
		t.Fatal(err)
	}
	assertSameCredentials(t, replay, first)
	otherTitle, otherTTL, otherShare, otherVerifier := original, original, original, original
	otherTitle.EncryptedTitle = chattest.Blob(chat.TitleBlobBytes)
	otherTTL.TTLMinutes = 31
	otherShare.KeyShare = chattest.Share()
	otherVerifier.JoinVerifier = chattest.Blob(chat.MACBytes)
	for _, input := range []chat.CreateInput{otherTitle, otherTTL, otherShare, otherVerifier} {
		if _, err := s.Create(testContext, input); !errors.Is(err, chat.ErrIdempotencyConflict) {
			t.Fatalf("changed request: %v", err)
		}
	}
	if _, err := s.Join(testContext, joinInput(first.InviteToken, key)); !errors.Is(err, chat.ErrIdempotencyConflict) {
		t.Fatalf("changed endpoint: %v", err)
	}
}

func TestJoinIdempotencyReplayReturnsTheSameSession(t *testing.T) {
	s, creator := newCreatedService(t)
	input := joinInput(creator.InviteToken, strings.Repeat("b", 32))
	first, err := s.Join(testContext, input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := New(s.pool, testEncryptionKey).Join(testContext, input)
	if err != nil {
		t.Fatal(err)
	}
	assertSameCredentials(t, replay, first)
	if first.InviteToken != "" || first.Self.Name == creator.Self.Name || len(first.Room.Participants) != 2 {
		t.Fatal("invalid guest credentials")
	}
}

func TestJoinIdempotencyFingerprintConflict(t *testing.T) {
	s, creator := newCreatedService(t)
	key := strings.Repeat("c", 32)
	if _, err := s.Join(testContext, joinInput(creator.InviteToken, key)); err != nil {
		t.Fatal(err)
	}
	other := createRoom(t, s, "")
	if _, err := s.Join(testContext, joinInput(other.InviteToken, key)); !errors.Is(err, chat.ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	if _, err := s.Join(testContext, chattest.JoinInput(other.InviteToken)); err != nil {
		t.Fatalf("conflict consumed another invite: %v", err)
	}
}

// Observe PostgreSQL's lock wait rather than relying on goroutine timing.
func waitForAdvisoryWaiter(t *testing.T, pool *pgxpool.Pool, lock int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(testContext, 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype = 'advisory' AND NOT granted AND classid::bigint = $1 AND objid::bigint = $2 AND objsubid = 1)`, int64(uint64(lock)>>32), int64(uint64(lock)&0xffffffff)).Scan(&waiting)
		if err != nil {
			t.Fatalf("waiting for advisory lock: %v", err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("operation did not wait for advisory lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestRoomCapacityUsesAnAdvisoryLock(t *testing.T) {
	s := newTestService(t)
	if _, err := s.pool.Exec(testContext, `INSERT INTO rooms (id, encrypted_title, status, expires_at) SELECT 'seed-' || n, 'R', 'open', now() + interval '30 minutes' FROM generate_series(1, 99) n`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(testContext)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(testContext)
	if _, err := tx.Exec(testContext, `SELECT pg_advisory_xact_lock(982463118)`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(testContext, 10*time.Second)
	defer cancel()
	results := make(chan error, 32)
	go func() {
		_, err := s.Create(ctx, chattest.CreateInput(30))
		results <- err
	}()
	waitForAdvisoryWaiter(t, s.pool, 982463118)
	for range 31 {
		go func() {
			_, err := s.Create(ctx, chattest.CreateInput(30))
			results <- err
		}()
	}
	if err := tx.Commit(testContext); err != nil {
		t.Fatal(err)
	}
	success := 0
	for range 32 {
		if err := <-results; err == nil {
			success++
		} else if !errors.Is(err, chat.ErrCapacity) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("created at capacity = %d", success)
	}
}

func TestConcurrentIdempotencyFirstUse(t *testing.T) {
	for _, endpoint := range []string{"create", "join"} {
		t.Run(endpoint, func(t *testing.T) {
			s := newTestService(t)
			key := strings.Repeat("k", 32)
			var invite string
			if endpoint == "join" {
				invite = createRoom(t, s, "").InviteToken
			}
			ctx, cancel := context.WithTimeout(testContext, 10*time.Second)
			defer cancel()
			request := func() (chat.Credentials, error) {
				if endpoint == "join" {
					return s.Join(ctx, joinInput(invite, key))
				}
				return s.Create(ctx, createInput(key))
			}
			tx, err := s.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(testContext)
			hash := sha256.Sum256([]byte(key))
			lock := int64(binary.BigEndian.Uint64(hash[:8]))
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lock); err != nil {
				t.Fatal(err)
			}
			type result struct {
				c   chat.Credentials
				err error
			}
			results := make(chan result, 16)
			go func() { c, err := request(); results <- result{c, err} }()
			waitForAdvisoryWaiter(t, s.pool, lock)
			for range 15 {
				go func() { c, err := request(); results <- result{c, err} }()
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			first := <-results
			if first.err != nil {
				t.Fatal(first.err)
			}
			for range 15 {
				next := <-results
				if next.err != nil {
					t.Fatal(next.err)
				}
				assertSameCredentials(t, next.c, first.c)
			}
			var rooms, records int
			if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM rooms), (SELECT count(*) FROM idempotency_requests)`).Scan(&rooms, &records); err != nil {
				t.Fatal(err)
			}
			if rooms != 1 || records != 1 {
				t.Fatalf("rooms = %d, replay records = %d", rooms, records)
			}
		})
	}
}

// Observe a real database lock wait, rather than assuming a goroutine has run.
func waitForBlockedTransaction(t *testing.T, pool *pgxpool.Pool, blocker uint32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(testContext, 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::int = ANY(pg_blocking_pids(pid)))`, int64(blocker)).Scan(&waiting); err != nil {
			t.Fatalf("operation did not wait for transaction: %v", err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("operation did not wait for transaction")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestJoinLocksRoomBeforeInvite(t *testing.T) {
	s, creator := newCreatedService(t)
	ctx, cancel := context.WithTimeout(testContext, 10*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err := tx.Exec(ctx, `SELECT id FROM rooms WHERE id = $1 FOR UPDATE`, creator.Room.ID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		credentials chat.Credentials
		err         error
	}
	done := make(chan result, 1)
	go func() {
		c, err := s.Join(ctx, chattest.JoinInput(creator.InviteToken))
		done <- result{c, err}
	}()
	waitForBlockedTransaction(t, s.pool, tx.Conn().PgConn().PID())
	// A room-first terminal operation must still be able to acquire the invite.
	// Bound the old invite-first deadlock without depending on deadlock_timeout.
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '1s'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE invites SET consumed_at = clock_timestamp() WHERE room_id = $1`, creator.Room.ID); err != nil {
		t.Fatalf("Join held the invite while waiting for the room: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if !errors.Is(got.err, chat.ErrUnavailable) || !reflect.DeepEqual(got.credentials, chat.Credentials{}) {
		t.Fatalf("Join did not revalidate the consumed invite: %v", got.err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM participants WHERE room_id = $1`, creator.Room.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("consumed invite admitted a guest: count = %d, error = %v", count, err)
	}
}

func TestRoomCapacityRechecksTimeAfterAdvisoryWait(t *testing.T) {
	s := newTestService(t)
	ctx, cancel := context.WithTimeout(testContext, 10*time.Second)
	defer cancel()
	if _, err := s.pool.Exec(ctx, `INSERT INTO rooms (id, encrypted_title, status, expires_at) SELECT 'seed-' || n, 'R', 'open', clock_timestamp() + interval '30 minutes' FROM generate_series(1, 100) n`); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(982463118)`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Create(ctx, chattest.CreateInput(30))
		done <- err
	}()
	waitForAdvisoryWaiter(t, s.pool, 982463118)
	// The new expiry is after Create's transaction start, but before its count.
	if _, err := tx.Exec(ctx, `UPDATE rooms SET expires_at = clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("expired rooms still consumed capacity after lock wait: %v", err)
	}
}

type snapshotGateKey struct{}
type snapshotGate struct {
	reached chan uint32
	release chan struct{}
}
type snapshotGateTracer struct{}

func (snapshotGateTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if gate, ok := ctx.Value(snapshotGateKey{}).(*snapshotGate); ok && strings.HasPrefix(data.SQL, "SELECT sequence,") {
		gate.reached <- conn.PgConn().PID()
		select {
		case <-gate.release:
		case <-ctx.Done():
		}
	}
	return ctx
}

func (snapshotGateTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestSubscribeJoinControlledHandoff(t *testing.T) {
	original, creator := newCreatedService(t)
	cfg := original.pool.Config()
	cfg.ConnConfig.Tracer = snapshotGateTracer{}
	pool, err := pgxpool.NewWithConfig(testContext, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := New(pool, testEncryptionKey)
	ctx, cancel := context.WithTimeout(testContext, 10*time.Second)
	defer cancel()
	gate := &snapshotGate{reached: make(chan uint32, 1), release: make(chan struct{})}
	defer func() {
		select {
		case <-gate.release:
		default:
			close(gate.release)
		}
	}()
	type subscription struct {
		events      <-chan chat.Event
		unsubscribe func()
		err         error
	}
	subscribed := make(chan subscription, 1)
	go func() {
		events, unsubscribe, err := s.Subscribe(context.WithValue(ctx, snapshotGateKey{}, gate), creator.SessionToken)
		subscribed <- subscription{events, unsubscribe, err}
	}()
	var pid uint32
	select {
	case pid = <-gate.reached:
	case <-ctx.Done():
		t.Fatal("Subscribe did not reach snapshot read")
	}
	joined := make(chan error, 1)
	go func() {
		_, err := s.Join(ctx, chattest.JoinInput(creator.InviteToken))
		joined <- err
	}()
	// Subscribe has read participants but has not registered with the hub yet.
	// Join must wait, or its participant update could be lost at this boundary.
	waitForBlockedTransaction(t, pool, pid)
	close(gate.release)
	sub := <-subscribed
	if sub.err != nil {
		t.Fatal(sub.err)
	}
	defer sub.unsubscribe()
	if err := <-joined; err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"snapshot", "participants"} {
		select {
		case event := <-sub.events:
			if event.Type != want {
				t.Fatalf("event = %q, want %q", event.Type, want)
			}
			if want == "snapshot" {
				snapshot := event.Payload.(chat.SnapshotEvent)
				if len(snapshot.Room.Participants) != 1 || snapshot.Self != creator.Self {
					t.Fatal("snapshot did not capture state before Join")
				}
			} else if participants := event.Payload.([]chat.Participant); len(participants) != 2 || participants[0] != creator.Self {
				t.Fatal("live event did not deliver joined participants")
			}
		case <-ctx.Done():
			t.Fatalf("missing %s event", want)
		}
	}
	select {
	case event := <-sub.events:
		t.Fatalf("duplicate event: %s", event.Type)
	default:
	}
}

func TestInviteDoesNotRevealRoomAfterUse(t *testing.T) {
	s, creator := newCreatedService(t)
	if _, err := s.Join(testContext, chattest.JoinInput(creator.InviteToken)); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{creator.InviteToken, strings.Repeat("0", 64), "malformed", ""} {
		got, err := s.Join(testContext, chattest.JoinInput(token))
		if !errors.Is(err, chat.ErrUnavailable) || !reflect.DeepEqual(got, chat.Credentials{}) {
			t.Fatalf("unusable invite must return empty credentials and room_unavailable; error = %v", err)
		}
	}
}

func TestCredentialsAreNotStoredInPlaintext(t *testing.T) {
	s := newTestService(t)
	key := strings.Repeat("d", 32)
	c := createRoom(t, s, key)
	for _, token := range []string{c.SessionToken, c.InviteToken, c.Room.ID, c.Self.ID} {
		raw, err := hex.DecodeString(token)
		if err != nil || len(raw) != 32 || strings.ToLower(token) != token {
			t.Fatal("credential/ID is not 32-byte lowercase hex")
		}
	}
	if c.SessionToken == c.InviteToken {
		t.Fatal("session and invite reused a secret")
	}
	var sessionHash, inviteHash, keyHash, ciphertext []byte
	var expires, roomExpires time.Time
	if err := s.pool.QueryRow(testContext, `SELECT s.token_hash, i.token_hash, q.key_hash, q.response_ciphertext, q.expires_at, r.expires_at FROM rooms r JOIN sessions s ON s.room_id = r.id JOIN invites i ON i.room_id = r.id JOIN idempotency_requests q ON q.room_id = r.id`).Scan(&sessionHash, &inviteHash, &keyHash, &ciphertext, &expires, &roomExpires); err != nil {
		t.Fatal(err)
	}
	for i, secret := range []string{c.SessionToken, c.InviteToken, key} {
		want := sha256.Sum256([]byte(secret))
		if !bytes.Equal([][]byte{sessionHash, inviteHash, keyHash}[i], want[:]) {
			t.Fatal("stored hash differs from SHA-256")
		}
		if bytes.Contains(ciphertext, []byte(secret)) {
			t.Fatal("plaintext secret in replay ciphertext")
		}
	}
	if !expires.Equal(roomExpires) {
		t.Fatal("replay retention exceeds room lifetime")
	}
	block, err := aes.NewCipher(testEncryptionKey[:])
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) < gcm.NonceSize()+gcm.Overhead() {
		t.Fatal("missing nonce or authentication tag")
	}
	plaintext, err := gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
	if err != nil {
		t.Fatal("replay is not an authenticated AES-GCM envelope")
	}
	var decoded chat.Credentials
	if err := json.Unmarshal(plaintext, &decoded); err != nil {
		t.Fatal(err)
	}
	assertSameCredentials(t, decoded, c)
	second := createRoom(t, s, strings.Repeat("e", 32))
	var other []byte
	if err := s.pool.QueryRow(testContext, `SELECT response_ciphertext FROM idempotency_requests WHERE room_id = $1`, second.Room.ID).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ciphertext[:gcm.NonceSize()], other[:gcm.NonceSize()]) {
		t.Fatal("nonce reused")
	}
}

func TestIdempotencyReplayUnavailable(t *testing.T) {
	for _, change := range []string{
		`UPDATE rooms SET status = 'closed'`,
		`UPDATE rooms SET expires_at = clock_timestamp() - interval '1 second'`,
		`UPDATE idempotency_requests SET expires_at = clock_timestamp() - interval '1 second'`,
	} {
		t.Run(change, func(t *testing.T) {
			s := newTestService(t)
			key := strings.Repeat("f", 32)
			creator := createRoom(t, s, key)
			input := joinInput(creator.InviteToken, strings.Repeat("g", 32))
			if _, err := s.Join(testContext, input); err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(testContext, change); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Create(testContext, createInput(key)); !errors.Is(err, chat.ErrUnavailable) {
				t.Fatalf("create replay: %v", err)
			}
			if _, err := s.Join(testContext, input); !errors.Is(err, chat.ErrUnavailable) {
				t.Fatalf("join replay: %v", err)
			}
		})
	}
}

func TestExpireDeletesExpiredIdempotencyAndAllowsKeyReuse(t *testing.T) {
	s := newTestService(t)
	key := strings.Repeat("r", 32)
	first := createRoom(t, s, key)

	if _, err := s.pool.Exec(testContext, `UPDATE idempotency_requests SET expires_at = clock_timestamp() - interval '1 second' WHERE room_id = $1`, first.Room.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(testContext, createInput(key)); !errors.Is(err, chat.ErrUnavailable) {
		t.Fatalf("expired replay before cleanup = %v, want room_unavailable", err)
	}

	if err := s.Expire(testContext); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err := s.pool.QueryRow(testContext, `SELECT count(*) FROM idempotency_requests WHERE room_id = $1`, first.Room.ID).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained != 0 {
		t.Fatalf("expired idempotency rows retained = %d, want 0", retained)
	}

	reused, err := s.Create(testContext, createInput(key))
	if err != nil {
		t.Fatalf("reuse after cleanup: %v", err)
	}
	if reused.Room.ID == first.Room.ID {
		t.Fatal("key reuse replayed expired credentials")
	}
}

func TestExpireCleanupPreservesReplayLockOrder(t *testing.T) {
	s := newTestService(t)
	key := strings.Repeat("l", 32)
	creator := createRoom(t, s, key)
	if _, err := s.pool.Exec(testContext, `UPDATE rooms SET expires_at = clock_timestamp() - interval '1 second' WHERE id = $1`, creator.Room.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(testContext, `UPDATE idempotency_requests SET expires_at = clock_timestamp() - interval '1 second' WHERE room_id = $1`, creator.Room.ID); err != nil {
		t.Fatal(err)
	}

	tx, err := s.pool.Begin(testContext)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	keyHash := digest(key)
	if _, err := tx.Exec(testContext, `SELECT key_hash FROM idempotency_requests WHERE key_hash = $1 FOR UPDATE`, keyHash[:]); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(testContext, 5*time.Second)
	defer cancel()
	expired := make(chan error, 1)
	go func() { expired <- s.Expire(ctx) }()
	for {
		var waiting bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM pg_stat_activity
WHERE datname = current_database()
  AND query LIKE 'DELETE FROM idempotency_requests%'
  AND wait_event_type = 'Lock'
)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("expiry cleanup did not wait on the replay row lock")
		case <-time.After(5 * time.Millisecond):
		}
	}

	_, roomLockErr := tx.Exec(ctx, `SELECT id FROM rooms WHERE id = $1 FOR SHARE`, creator.Room.ID)
	commitErr := tx.Commit(ctx)
	expireErr := <-expired
	if roomLockErr != nil || commitErr != nil || expireErr != nil {
		t.Fatalf("replay/cleanup lock order: room lock = %v, commit = %v, expire = %v", roomLockErr, commitErr, expireErr)
	}
}

func TestSubscribeSnapshotAndParticipants(t *testing.T) {
	s, creator := newCreatedService(t)
	for _, seq := range []int{2, 1} {
		if _, err := s.pool.Exec(testContext, `INSERT INTO messages (id, room_id, participant_id, client_message_id, sequence, ciphertext, created_at) VALUES ($1, $2, $3, $1, $4, 'opaque', now())`, string(rune('a'+seq)), creator.Room.ID, creator.Self.ID, seq); err != nil {
			t.Fatal(err)
		}
	}
	events, unsubscribe, err := s.Subscribe(testContext, creator.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	select {
	case event := <-events:
		snapshot, ok := event.Payload.(chat.SnapshotEvent)
		if event.Type != "snapshot" || !ok || snapshot.Self != creator.Self || snapshot.Room.ID != creator.Room.ID {
			t.Fatal("invalid initial snapshot")
		}
		if len(snapshot.Room.Messages) != 2 || snapshot.Room.Messages[0].Sequence != 1 || snapshot.Room.Messages[1].Sequence != 2 {
			t.Fatal("snapshot history not ordered")
		}
	case <-time.After(time.Second):
		t.Fatal("missing snapshot")
	}
	guest, err := s.Join(testContext, chattest.JoinInput(creator.InviteToken))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Type != "participants" || !reflect.DeepEqual(event.Payload, guest.Room.Participants) {
			t.Fatal("invalid participants event")
		}
		var count int
		if err := s.pool.QueryRow(testContext, `SELECT count(*) FROM sessions`).Scan(&count); err != nil || count != 2 {
			t.Fatalf("join event preceded commit: %d, %v", count, err)
		}
	case <-time.After(time.Second):
		t.Fatal("missing participants event")
	}
	_, cancelSecond, err := s.Subscribe(testContext, creator.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelSecond()
	if _, _, err := s.Subscribe(testContext, creator.SessionToken); !errors.Is(err, chat.ErrConnectionLimit) {
		t.Fatalf("connection limit: %v", err)
	}
	unsubscribe()
	unsubscribe()
	if _, open := <-events; open {
		t.Fatal("unsubscribe did not close channel")
	}
	_, cancelThird, err := s.Subscribe(testContext, creator.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	cancelThird()
}

func TestSubscribeRejectsInvalidSessions(t *testing.T) {
	for _, change := range []string{
		`UPDATE sessions SET revoked_at = now()`,
		`UPDATE sessions SET expires_at = now() - interval '1 second'`,
		`UPDATE rooms SET status = 'closed'`,
		`UPDATE rooms SET expires_at = now() - interval '1 second'`,
	} {
		t.Run(change, func(t *testing.T) {
			s, creator := newCreatedService(t)
			if _, err := s.pool.Exec(testContext, change); err != nil {
				t.Fatal(err)
			}
			for _, token := range []string{creator.SessionToken, strings.Repeat("0", 64), "bad"} {
				ch, unsub, err := s.Subscribe(testContext, token)
				if !errors.Is(err, chat.ErrSession) || ch != nil || unsub != nil {
					t.Fatalf("invalid session: %v", err)
				}
			}
		})
	}
}

func TestCreateValidatesBeforeDatabase(t *testing.T) {
	s := New(nil, testEncryptionKey)
	plaintext, zero, long := chattest.CreateInput(30), chattest.CreateInput(0), chattest.CreateInput(61)
	plaintext.EncryptedTitle = strings.Repeat("x", 61)
	for _, input := range []chat.CreateInput{plaintext, zero, long} {
		if _, err := s.Create(testContext, input); !errors.Is(err, chat.ErrInput) {
			t.Fatal(err)
		}
	}
}

// Cancel just before pgx sends COMMIT, after the real scenario has performed
// every query (and Subscribe has registered its hub subscription).
type cancelCommitContextKey struct{}
type cancelCommitTracer struct{}

func (cancelCommitTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if cancel, ok := ctx.Value(cancelCommitContextKey{}).(context.CancelFunc); ok && data.SQL == "commit" {
		cancel()
	}
	return ctx
}

func (cancelCommitTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func serviceWithCommitCancellation(t *testing.T, s *Service) *Service {
	t.Helper()
	cfg := s.pool.Config()
	cfg.ConnConfig.Tracer = cancelCommitTracer{}
	pool, err := pgxpool.NewWithConfig(testContext, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return New(pool, testEncryptionKey)
}

func cancelAtCommit(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(testContext, 5*time.Second)
	t.Cleanup(cancel)
	return context.WithValue(ctx, cancelCommitContextKey{}, context.CancelFunc(cancel))
}

func TestSubscribeCommitFailureUnsubscribes(t *testing.T) {
	original, creator := newCreatedService(t)
	s := serviceWithCommitCancellation(t, original)
	for range 3 {
		ch, unsub, err := s.Subscribe(cancelAtCommit(t), creator.SessionToken)
		if !errors.Is(err, chat.ErrDatabaseUnavailable) || ch != nil || unsub != nil {
			t.Fatalf("failed subscription: %v", err)
		}
	}
	// Leaking even one subscription would prevent two subsequent connections.
	for range 2 {
		_, unsubscribe, err := s.Subscribe(testContext, creator.SessionToken)
		if err != nil {
			t.Fatalf("failed commit leaked a connection slot: %v", err)
		}
		defer unsubscribe()
	}
}

func TestCreateCommitFailureRollsBack(t *testing.T) {
	s := serviceWithCommitCancellation(t, newTestService(t))
	input := createInput(strings.Repeat("z", 32))
	got, err := s.Create(cancelAtCommit(t), input)
	if !errors.Is(err, chat.ErrDatabaseUnavailable) || !reflect.DeepEqual(got, chat.Credentials{}) {
		t.Fatalf("failed create: %v", err)
	}
	var rows int
	if err := s.pool.QueryRow(testContext, `SELECT (SELECT count(*) FROM rooms) + (SELECT count(*) FROM participants) + (SELECT count(*) FROM sessions) + (SELECT count(*) FROM invites) + (SELECT count(*) FROM idempotency_requests)`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("rollback retained %d rows", rows)
	}
	if _, err := s.Create(testContext, input); err != nil {
		t.Fatalf("rolled-back key cannot be retried: %v", err)
	}
}

func TestJoinCommitFailureDoesNotConsumeOrPublish(t *testing.T) {
	original, creator := newCreatedService(t)
	s := serviceWithCommitCancellation(t, original)
	events, unsubscribe, err := s.Subscribe(testContext, creator.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	<-events // Initial snapshot.
	input := joinInput(creator.InviteToken, strings.Repeat("y", 32))
	got, err := s.Join(cancelAtCommit(t), input)
	if !errors.Is(err, chat.ErrDatabaseUnavailable) || !reflect.DeepEqual(got, chat.Credentials{}) {
		t.Fatalf("failed join: %v", err)
	}
	select {
	case <-events:
		t.Fatal("failed join published an event")
	default:
	}
	if _, err := s.Join(testContext, input); err != nil {
		t.Fatalf("failed commit consumed invite or replay key: %v", err)
	}
}

func TestCredentialEnvelopeAuthenticatesAndUsesFreshNonce(t *testing.T) {
	s := New(nil, testEncryptionKey)
	c := chat.Credentials{SessionToken: "session", InviteToken: "invite"}
	first, err := s.encryptCredentials(c)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.encryptCredentials(c)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first[:12], second[:12]) {
		t.Fatal("same envelope reused a nonce")
	}
	decoded, err := s.decryptCredentials(first)
	if err != nil {
		t.Fatal(err)
	}
	assertSameCredentials(t, decoded, c)
	for _, corrupt := range [][]byte{nil, first[:11], append([]byte(nil), first...)} {
		if len(corrupt) == len(first) {
			corrupt[len(corrupt)-1] ^= 1
		}
		if _, err := s.decryptCredentials(corrupt); err == nil {
			t.Fatal("accepted corrupt envelope")
		}
	}
	if _, err := New(nil, [32]byte{9}).decryptCredentials(first); err == nil {
		t.Fatal("accepted wrong encryption key")
	}
}

func TestAdmissionDatabaseErrorsAreSanitized(t *testing.T) {
	original, creator := newCreatedService(t)
	s := serviceWithCommitCancellation(t, original)
	s.pool.Close()
	if _, err := s.Create(testContext, chattest.CreateInput(30)); err != chat.ErrDatabaseUnavailable {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Join(testContext, chattest.JoinInput(creator.InviteToken)); err != chat.ErrDatabaseUnavailable {
		t.Fatalf("join: %v", err)
	}
	if _, _, err := s.Subscribe(testContext, creator.SessionToken); err != chat.ErrDatabaseUnavailable {
		t.Fatalf("subscribe: %v", err)
	}
}

func TestWrongJoinProofIsCommittedWithoutConsumingInvite(t *testing.T) {
	s, creator := newCreatedService(t)
	wrong := chattest.JoinInput(creator.InviteToken)
	wrong.JoinProof = chattest.Blob(chat.MACBytes)
	for range chat.MaxJoinAttempts - 1 {
		if _, err := New(s.pool, testEncryptionKey).Join(testContext, wrong); !errors.Is(err, chat.ErrJoinProof) {
			t.Fatalf("wrong proof: %v", err)
		}
	}
	var failed int
	if err := s.pool.QueryRow(testContext, `SELECT failed_joins FROM rooms WHERE id = $1`, creator.Room.ID).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if failed != chat.MaxJoinAttempts-1 {
		t.Fatalf("failed_joins = %d, want %d", failed, chat.MaxJoinAttempts-1)
	}
	guest, err := s.Join(testContext, chattest.JoinInput(creator.InviteToken))
	if err != nil {
		t.Fatalf("correct proof after failures: %v", err)
	}
	if guest.Self.Role != chat.RoleGuest || guest.Room.Participants[0].Role != chat.RoleCreator || guest.Room.Participants[0].KeyShare != creator.Self.KeyShare {
		t.Fatal("roles or key shares not persisted")
	}
}

func TestJoinProofFailuresRevokeInviteAndNotify(t *testing.T) {
	s, creator := newCreatedService(t)
	events, unsubscribe, err := s.Subscribe(testContext, creator.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	receiveEvent(t, events, "snapshot")
	wrong := chattest.JoinInput(creator.InviteToken)
	wrong.JoinProof = chattest.Blob(chat.MACBytes)
	for range chat.MaxJoinAttempts {
		if _, err := s.Join(testContext, wrong); !errors.Is(err, chat.ErrJoinProof) {
			t.Fatalf("wrong proof: %v", err)
		}
	}
	receiveEvent(t, events, "invite_revoked")
	if _, err := s.Join(testContext, chattest.JoinInput(creator.InviteToken)); !errors.Is(err, chat.ErrUnavailable) {
		t.Fatalf("revoked invite: %v", err)
	}
}

func TestEncryptedFieldsStoredVerbatim(t *testing.T) {
	s := newTestService(t)
	input := createInput("")
	c, err := s.Create(testContext, input)
	if err != nil {
		t.Fatal(err)
	}
	var title, publicKey, mac string
	var verifier []byte
	if err := s.pool.QueryRow(testContext, `SELECT r.encrypted_title, r.join_verifier_hash, p.public_key, p.key_mac FROM rooms r JOIN participants p ON p.room_id = r.id WHERE r.id = $1`, c.Room.ID).Scan(&title, &verifier, &publicKey, &mac); err != nil {
		t.Fatal(err)
	}
	if title != input.EncryptedTitle || publicKey != input.KeyShare.PublicKey || mac != input.KeyShare.MAC {
		t.Fatal("encrypted fields altered in storage")
	}
	if bytes.Contains(verifier, []byte(input.JoinVerifier)) || len(verifier) != 32 {
		t.Fatal("join verifier must be stored only as a SHA-256 digest")
	}
}
