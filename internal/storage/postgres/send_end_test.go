package postgres

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"nullchat/internal/chat"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func receiveEvent(t *testing.T, events <-chan chat.Event, want string) chat.Event {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok || event.Type != want {
			t.Fatalf("event = %q, open = %v; want %q", event.Type, ok, want)
		}
		return event
	case <-time.After(5 * time.Second):
		t.Fatalf("missing %s event", want)
		return chat.Event{}
	}
}

func subscribeRoom(t *testing.T, s *Service, secret string) <-chan chat.Event {
	t.Helper()
	events, cancel, err := s.Subscribe(testContext, secret)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	receiveEvent(t, events, "snapshot")
	return events
}

func noEvent(t *testing.T, events <-chan chat.Event) {
	t.Helper()
	select {
	case event, ok := <-events:
		t.Fatalf("unexpected event or closure: %+v, open = %v", event, ok)
	default:
	}
}

func TestDuplicateMessageIsStoredOnce(t *testing.T) {
	s, creator := newCreatedService(t)
	events := subscribeRoom(t, s, creator.SessionToken)
	first, err := s.Send(testContext, creator.SessionToken, "stable-id", " hello ")
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || first.Text != "hello" || first.SenderID != creator.Self.ID || first.ClientID != "stable-id" || !validToken(first.ID) || first.CreatedAt.IsZero() {
		t.Fatalf("message = %+v", first)
	}
	if got := receiveEvent(t, events, "message").Payload.(chat.Message); !reflect.DeepEqual(got, first) {
		t.Fatalf("published = %+v, want %+v", got, first)
	}
	// Concurrent retries must bypass the rate limit, preserve the entire message,
	// and neither store nor publish a duplicate.
	results := make(chan error, 24)
	for range 24 {
		go func() {
			again, err := s.Send(testContext, creator.SessionToken, "stable-id", "hello")
			if err == nil && !reflect.DeepEqual(again, first) {
				err = fmt.Errorf("retry differs: %+v", again)
			}
			results <- err
		}()
	}
	for range 24 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Send(testContext, creator.SessionToken, "stable-id", "changed"); !errors.Is(err, chat.ErrConflict) {
		t.Fatal(err)
	}
	var rows, count, next int
	if err := s.pool.QueryRow(testContext, `SELECT (SELECT count(*) FROM messages), message_count, next_message_sequence FROM rooms WHERE id = $1`, creator.Room.ID).Scan(&rows, &count, &next); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || count != 1 || next != 2 {
		t.Fatalf("rows/count/next = %d/%d/%d", rows, count, next)
	}
	noEvent(t, events)
}

func TestSubscribeAndSendHaveNoGap(t *testing.T) {
	s := newTestService(t)
	for i := range 50 {
		creator := createRoom(t, s, "")
		done := make(chan error, 1)
		go func() {
			_, err := s.Send(testContext, creator.SessionToken, "one", "race")
			done <- err
		}()
		events, cancel, err := s.Subscribe(testContext, creator.SessionToken)
		if err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			cancel()
			t.Fatal(err)
		}
		snapshot := receiveEvent(t, events, "snapshot").Payload.(chat.SnapshotEvent)
		count := len(snapshot.Room.Messages)
		if count == 0 {
			receiveEvent(t, events, "message")
			count++
		}
		noEvent(t, events)
		cancel()
		if count != 1 {
			t.Fatalf("iteration %d: message count = %d", i, count)
		}
	}
}

// Pause a real query before execution or after commit to exercise both sides
// of the snapshot/publication boundary without production test hooks.
type operationGateKey struct{}
type operationGate struct {
	sql     string
	after   bool
	reached chan uint32
	release chan struct{}
}
type operationGateTracer struct{}

func pauseOperation(ctx context.Context, conn *pgx.Conn, gate *operationGate) {
	gate.reached <- conn.PgConn().PID()
	select {
	case <-gate.release:
	case <-ctx.Done():
	}
}

func (operationGateTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	gate, ok := ctx.Value(operationGateKey{}).(*operationGate)
	if !ok || !strings.HasPrefix(data.SQL, gate.sql) {
		return context.WithValue(ctx, operationGateKey{}, (*operationGate)(nil))
	}
	if !gate.after {
		pauseOperation(ctx, conn, gate)
	}
	return ctx
}

func (operationGateTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	if gate, _ := ctx.Value(operationGateKey{}).(*operationGate); gate != nil && gate.after && data.Err == nil {
		pauseOperation(ctx, conn, gate)
	}
}

func TestSubscribeSendControlledHandoff(t *testing.T) {
	for _, writerFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("writerFirst=%v", writerFirst), func(t *testing.T) {
			original, creator := newCreatedService(t)
			cfg := original.pool.Config()
			cfg.ConnConfig.Tracer = operationGateTracer{}
			pool, err := pgxpool.NewWithConfig(testContext, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			s := New(pool, testEncryptionKey)
			ctx, cancel := context.WithTimeout(testContext, 10*time.Second)
			defer cancel()
			gate := &operationGate{sql: "SELECT sequence,", reached: make(chan uint32, 1), release: make(chan struct{})}
			if writerFirst {
				gate.sql, gate.after = "commit", true
			}
			defer func() {
				select {
				case <-gate.release:
				default:
					close(gate.release)
				}
			}()
			type subscription struct {
				events <-chan chat.Event
				cancel func()
				err    error
			}
			subscribed := make(chan subscription, 1)
			sent := make(chan error, 1)
			subscribe := func(ctx context.Context) {
				events, cancel, err := s.Subscribe(ctx, creator.SessionToken)
				subscribed <- subscription{events, cancel, err}
			}
			send := func(ctx context.Context) { _, err := s.Send(ctx, creator.SessionToken, "one", "boundary"); sent <- err }
			gated := context.WithValue(ctx, operationGateKey{}, gate)
			if writerFirst {
				go send(gated)
			} else {
				go subscribe(gated)
			}
			var pid uint32
			select {
			case pid = <-gate.reached:
			case <-ctx.Done():
				t.Fatal("operation did not reach gate")
			}
			if writerFirst {
				// Send committed but still holds the handoff mutex. Subscribe can
				// read the committed row, but cannot register until publication.
				snapshotGate := &operationGate{sql: "SELECT sequence,", reached: make(chan uint32, 1), release: make(chan struct{})}
				close(snapshotGate.release)
				go subscribe(context.WithValue(ctx, operationGateKey{}, snapshotGate))
				select {
				case <-snapshotGate.reached:
				case <-ctx.Done():
					t.Fatal("snapshot did not reach committed message")
				}
			} else {
				go send(ctx)
				waitForBlockedTransaction(t, pool, pid)
			}
			close(gate.release)
			sub := <-subscribed
			if sub.err != nil {
				t.Fatal(sub.err)
			}
			defer sub.cancel()
			if err := <-sent; err != nil {
				t.Fatal(err)
			}
			snapshot := receiveEvent(t, sub.events, "snapshot").Payload.(chat.SnapshotEvent)
			if writerFirst {
				if len(snapshot.Room.Messages) != 1 || snapshot.Room.Messages[0].Text != "boundary" {
					t.Fatalf("snapshot = %+v", snapshot.Room.Messages)
				}
			} else {
				if len(snapshot.Room.Messages) != 0 {
					t.Fatal("snapshot includes blocked send")
				}
				if message := receiveEvent(t, sub.events, "message").Payload.(chat.Message); message.Sequence != 1 || message.Text != "boundary" {
					t.Fatalf("message = %+v", message)
				}
			}
			noEvent(t, sub.events)
		})
	}
}

func TestSendRechecksExpiryAfterRoomLock(t *testing.T) {
	for _, operation := range []string{"send", "end"} {
		t.Run(operation, func(t *testing.T) {
			s, creator := newCreatedService(t)
			tx, err := s.pool.Begin(testContext)
			if err != nil {
				t.Fatal(err)
			}
			defer rollback(tx)
			if _, err := tx.Exec(testContext, `SELECT id FROM rooms WHERE id = $1 FOR UPDATE`, creator.Room.ID); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(testContext, 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if operation == "end" {
					done <- s.End(ctx, creator.SessionToken)
					return
				}
				_, err := s.Send(ctx, creator.SessionToken, "late", "late")
				done <- err
			}()
			waitForBlockedTransaction(t, s.pool, tx.Conn().PgConn().PID())
			// This timestamp is AFTER the waiting transaction started. now()
			// in that transaction would incorrectly accept the expired room.
			if _, err := tx.Exec(testContext, `UPDATE rooms SET expires_at = clock_timestamp() WHERE id = $1`, creator.Room.ID); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(testContext); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, chat.ErrSession) {
				t.Fatalf("expired %s: %v", operation, err)
			}
			var count int
			var status string
			if err := s.pool.QueryRow(testContext, `SELECT message_count, status FROM rooms WHERE id = $1`, creator.Room.ID).Scan(&count, &status); err != nil {
				t.Fatal(err)
			}
			if count != 0 || status != "open" {
				t.Fatalf("rejected operation changed room: %d/%s", count, status)
			}
		})
	}
}

func TestSendLimitsAndRate(t *testing.T) {
	s, creator := newCreatedService(t)
	for _, input := range [][2]string{{"", "hello"}, {"id", "  "}, {strings.Repeat("i", 65), "hello"}, {"id", strings.Repeat("я", 2001)}, {"id", "\xff"}} {
		if _, err := s.Send(testContext, creator.SessionToken, input[0], input[1]); !errors.Is(err, chat.ErrInput) {
			t.Fatalf("invalid message: %v", err)
		}
	}
	first, err := s.Send(testContext, creator.SessionToken, "first", strings.Repeat("я", 2000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(testContext, creator.SessionToken, "fast", "fast"); !errors.Is(err, chat.ErrRate) {
		t.Fatalf("rate: %v", err)
	}
	if _, err := s.pool.Exec(testContext, `UPDATE sessions SET last_send_at = clock_timestamp() - interval '200 milliseconds' WHERE room_id = $1`, creator.Room.ID); err != nil {
		t.Fatal(err)
	}
	if message, err := s.Send(testContext, creator.SessionToken, "second", "second"); err != nil || message.Sequence != 2 {
		t.Fatalf("after interval: %+v, %v", message, err)
	}
	// Seed committed history to the last available slot without sleeping 100s.
	if _, err := s.pool.Exec(testContext, `INSERT INTO messages (id, room_id, participant_id, client_message_id, sequence, text, created_at) SELECT 'seed-' || n, $1, $2, 'seed-' || n, n, 'seed', clock_timestamp() FROM generate_series(3, 499) n`, creator.Room.ID, creator.Self.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(testContext, `UPDATE rooms SET message_count = 499, next_message_sequence = 500 WHERE id = $1`, creator.Room.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(testContext, `UPDATE sessions SET last_send_at = NULL WHERE room_id = $1`, creator.Room.ID); err != nil {
		t.Fatal(err)
	}
	if message, err := s.Send(testContext, creator.SessionToken, "last", "last"); err != nil || message.Sequence != 500 {
		t.Fatalf("last slot: %+v, %v", message, err)
	}
	if _, err := s.Send(testContext, creator.SessionToken, "overflow", "overflow"); !errors.Is(err, chat.ErrMessageLimit) {
		t.Fatalf("limit: %v", err)
	}
	if again, err := s.Send(testContext, creator.SessionToken, "first", first.Text); err != nil || again.ID != first.ID {
		t.Fatalf("retry at limit: %+v, %v", again, err)
	}
	var rows, count, next int
	if err := s.pool.QueryRow(testContext, `SELECT (SELECT count(*) FROM messages), message_count, next_message_sequence FROM rooms WHERE id = $1`, creator.Room.ID).Scan(&rows, &count, &next); err != nil {
		t.Fatal(err)
	}
	if rows != 500 || count != 500 || next != 501 {
		t.Fatalf("rows/count/next = %d/%d/%d", rows, count, next)
	}
}

func TestSendRejectsNULInputBeforeDatabaseAccess(t *testing.T) {
	s := &Service{}
	for name, input := range map[string][2]string{
		"client id": {"bad\x00id", "hello"},
		"message":   {"client", "hello\x00world"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Send(testContext, strings.Repeat("a", 64), input[0], input[1]); !errors.Is(err, chat.ErrInput) {
				t.Fatalf("error = %v, want %v", err, chat.ErrInput)
			}
		})
	}
}

func TestSendCanceledContextPrecedesInvalidInput(t *testing.T) {
	s := &Service{}
	ctx, cancel := context.WithCancel(testContext)
	cancel()
	if _, err := s.Send(ctx, strings.Repeat("a", 64), "bad\x00id", "hello\x00world"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want %v", err, context.Canceled)
	}

	deadline, cancel := context.WithDeadline(testContext, time.Now().Add(-time.Second))
	defer cancel()
	if _, err := s.Send(deadline, strings.Repeat("a", 64), "bad\x00id", "hello\x00world"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestConcurrentSendSequences(t *testing.T) {
	s, creator := newCreatedService(t)
	guest, err := s.Join(testContext, chat.JoinInput{InviteToken: creator.InviteToken})
	if err != nil {
		t.Fatal(err)
	}
	events := subscribeRoom(t, s, creator.SessionToken)
	for round := range 12 {
		if _, err := s.pool.Exec(testContext, `UPDATE sessions SET last_send_at = NULL WHERE room_id = $1`, creator.Room.ID); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, secret := range []string{creator.SessionToken, guest.SessionToken} {
			go func() {
				<-start
				_, err := s.Send(testContext, secret, fmt.Sprint(round), "concurrent")
				results <- err
			}()
		}
		close(start)
		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		for offset := range 2 {
			message := receiveEvent(t, events, "message").Payload.(chat.Message)
			if want := round*2 + offset + 1; message.Sequence != want {
				t.Fatalf("sequence = %d, want %d", message.Sequence, want)
			}
		}
	}
	ch, cancel, err := s.Subscribe(testContext, creator.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	messages := receiveEvent(t, ch, "snapshot").Payload.(chat.SnapshotEvent).Room.Messages
	if len(messages) != 24 {
		t.Fatalf("history = %d", len(messages))
	}
	for i, message := range messages {
		if message.Sequence != i+1 {
			t.Fatalf("stored sequence = %d", message.Sequence)
		}
	}
}

func assertRoomRevoked(t *testing.T, s *Service, creator, guest chat.Credentials, status string) {
	t.Helper()
	var got string
	var closed, revoked, consumed bool
	if err := s.pool.QueryRow(testContext, `SELECT status, closed_at IS NOT NULL,
NOT EXISTS (SELECT 1 FROM sessions WHERE room_id = $1 AND revoked_at IS NULL),
NOT EXISTS (SELECT 1 FROM invites WHERE room_id = $1 AND consumed_at IS NULL)
FROM rooms WHERE id = $1`, creator.Room.ID).Scan(&got, &closed, &revoked, &consumed); err != nil {
		t.Fatal(err)
	}
	if got != status || !closed || !revoked || !consumed {
		t.Fatalf("terminal state = %s/%v/%v/%v", got, closed, revoked, consumed)
	}
	for _, secret := range []string{creator.SessionToken, guest.SessionToken} {
		if _, err := s.Send(testContext, secret, "one", "hello"); !errors.Is(err, chat.ErrSession) {
			t.Fatalf("send after terminal: %v", err)
		}
		if err := s.End(testContext, secret); !errors.Is(err, chat.ErrSession) {
			t.Fatalf("end after terminal: %v", err)
		}
		if _, cancel, err := s.Subscribe(testContext, secret); !errors.Is(err, chat.ErrSession) {
			if cancel != nil {
				cancel()
			}
			t.Fatalf("subscribe after terminal: %v", err)
		}
	}
	if _, err := s.Join(testContext, chat.JoinInput{InviteToken: creator.InviteToken}); !errors.Is(err, chat.ErrUnavailable) {
		t.Fatalf("join after terminal: %v", err)
	}
}

func TestEndRevokesBothSessionsAndInvite(t *testing.T) {
	s, creator := newCreatedService(t)
	guest, err := s.Join(testContext, chat.JoinInput{InviteToken: creator.InviteToken})
	if err != nil {
		t.Fatal(err)
	}
	events := subscribeRoom(t, s, creator.SessionToken)
	other := subscribeRoom(t, s, guest.SessionToken)
	if _, err := s.Send(testContext, creator.SessionToken, "one", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := s.End(testContext, guest.SessionToken); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []<-chan chat.Event{events, other} {
		receiveEvent(t, ch, "room_closed")
		if _, ok := <-ch; ok {
			t.Fatal("terminal subscription remains open")
		}
	}
	assertRoomRevoked(t, s, creator, guest, "closed")
	// Also close a room whose invite has never been consumed by Join.
	unjoined := createRoom(t, s, "")
	if err := s.End(testContext, unjoined.SessionToken); err != nil {
		t.Fatal(err)
	}
	assertRoomRevoked(t, s, unjoined, unjoined, "closed")
}

func TestExpiryRevokesAccessWithoutPriorSweep(t *testing.T) {
	s, creator := newCreatedService(t)
	guest, err := s.Join(testContext, chat.JoinInput{InviteToken: creator.InviteToken})
	if err != nil {
		t.Fatal(err)
	}
	events := subscribeRoom(t, s, creator.SessionToken)
	if _, err := s.Send(testContext, creator.SessionToken, "one", "hello"); err != nil {
		t.Fatal(err)
	}
	receiveEvent(t, events, "message")
	if _, err := s.pool.Exec(testContext, `UPDATE rooms SET expires_at = clock_timestamp() - interval '1 second' WHERE id = $1`, creator.Room.ID); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{creator.SessionToken, guest.SessionToken} {
		if _, err := s.Send(testContext, secret, "one", "hello"); !errors.Is(err, chat.ErrSession) {
			t.Fatalf("send before sweep: %v", err)
		}
		if err := s.End(testContext, secret); !errors.Is(err, chat.ErrSession) {
			t.Fatalf("end before sweep: %v", err)
		}
		if _, cancel, err := s.Subscribe(testContext, secret); !errors.Is(err, chat.ErrSession) {
			if cancel != nil {
				cancel()
			}
			t.Fatalf("subscribe before sweep: %v", err)
		}
	}
	if err := s.Expire(testContext); err != nil {
		t.Fatal(err)
	}
	receiveEvent(t, events, "room_expired")
	if _, ok := <-events; ok {
		t.Fatal("expired subscription remains open")
	}
	assertRoomRevoked(t, s, creator, guest, "expired")
	var closedAt time.Time
	if err := s.pool.QueryRow(testContext, `SELECT closed_at FROM rooms WHERE id = $1`, creator.Room.ID).Scan(&closedAt); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(testContext); err != nil {
		t.Fatal(err)
	}
	var again time.Time
	if err := s.pool.QueryRow(testContext, `SELECT closed_at FROM rooms WHERE id = $1`, creator.Room.ID).Scan(&again); err != nil {
		t.Fatal(err)
	}
	if !closedAt.Equal(again) {
		t.Fatal("retry changed terminal timestamp")
	}
}

func TestExpireSkipsLockedRoomsAndDrainsBatches(t *testing.T) {
	s, locked := newCreatedService(t)
	open := createRoom(t, s, "")
	if _, err := s.pool.Exec(testContext, `INSERT INTO rooms (id, title, status, expires_at) SELECT 'expired-' || n, 'R', 'open', clock_timestamp() - interval '1 second' FROM generate_series(1, 205) n`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(testContext, `UPDATE rooms SET expires_at = clock_timestamp() - interval '1 second' WHERE id = $1`, locked.Room.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(testContext)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	if _, err := tx.Exec(testContext, `SELECT id FROM rooms WHERE id = $1 FOR UPDATE`, locked.Room.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(testContext, 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- s.Expire(ctx) }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.pool.QueryRow(testContext, `SELECT count(*) FROM rooms WHERE status = 'expired'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 205 {
		t.Fatalf("expired = %d, want 205", count)
	}
	if err := tx.Commit(testContext); err != nil {
		t.Fatal(err)
	}
	if err := s.Expire(testContext); err != nil {
		t.Fatal(err)
	}
	assertRoomRevoked(t, s, locked, locked, "expired")
	if _, err := s.Send(testContext, open.SessionToken, "still-open", "hello"); err != nil {
		t.Fatalf("unexpired room: %v", err)
	}
}

func TestOperationsCommitFailureDoesNotPublish(t *testing.T) {
	for _, operation := range []string{"send", "end", "expire"} {
		t.Run(operation, func(t *testing.T) {
			original, creator := newCreatedService(t)
			s := serviceWithCommitCancellation(t, original)
			events := subscribeRoom(t, s, creator.SessionToken)
			var err error
			switch operation {
			case "send":
				_, err = s.Send(cancelAtCommit(t), creator.SessionToken, "one", "hello")
			case "end":
				err = s.End(cancelAtCommit(t), creator.SessionToken)
			case "expire":
				if _, err := s.pool.Exec(testContext, `UPDATE rooms SET expires_at = clock_timestamp() - interval '1 second' WHERE id = $1`, creator.Room.ID); err != nil {
					t.Fatal(err)
				}
				err = s.Expire(cancelAtCommit(t))
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("commit cancellation = %v", err)
			}
			noEvent(t, events)
			var status string
			var count, next int
			var closed, revoked, consumed, sent bool
			if err := s.pool.QueryRow(testContext, `SELECT r.status, r.message_count, r.next_message_sequence, r.closed_at IS NOT NULL, s.revoked_at IS NOT NULL, i.consumed_at IS NOT NULL, s.last_send_at IS NOT NULL FROM rooms r JOIN sessions s ON s.room_id = r.id JOIN invites i ON i.room_id = r.id WHERE r.id = $1`, creator.Room.ID).Scan(&status, &count, &next, &closed, &revoked, &consumed, &sent); err != nil {
				t.Fatal(err)
			}
			if status != "open" || count != 0 || next != 1 || closed || revoked || consumed || sent {
				t.Fatalf("failed commit left state: %s/%d/%d/%v/%v/%v/%v", status, count, next, closed, revoked, consumed, sent)
			}
			if operation == "expire" {
				if err := s.Expire(testContext); err != nil {
					t.Fatal(err)
				}
				receiveEvent(t, events, "room_expired")
			} else {
				if _, err := s.Send(testContext, creator.SessionToken, "one", "hello"); err != nil {
					t.Fatalf("retry: %v", err)
				}
				receiveEvent(t, events, "message")
			}
		})
	}
}

func TestReadyAndOperationDatabaseErrors(t *testing.T) {
	s, creator := newCreatedService(t)
	if err := s.Ready(testContext); err != nil {
		t.Fatal(err)
	}
	operations := map[string]func(context.Context) error{
		"send": func(ctx context.Context) error {
			_, err := s.Send(ctx, creator.SessionToken, "one", "hello")
			return err
		},
		"end":    func(ctx context.Context) error { return s.End(ctx, creator.SessionToken) },
		"expire": s.Expire, "ready": s.Ready,
	}
	for name, operation := range operations {
		for _, deadline := range []bool{false, true} {
			ctx, cancel := context.WithCancel(testContext)
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithDeadline(testContext, time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			}
			cancel()
			if err := operation(ctx); !errors.Is(err, want) {
				t.Fatalf("%s cancellation = %v, want %v", name, err, want)
			}
		}
	}
	// Use a separate closed pool so the fixture's cleanup can still run.
	pool, err := pgxpool.NewWithConfig(testContext, s.pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	s.pool = pool
	for name, operation := range operations {
		if err := operation(testContext); err != chat.ErrDatabaseUnavailable {
			t.Fatalf("%s database failure = %v", name, err)
		}
	}
}

func TestClosePreservesRoomsSessionsAndPool(t *testing.T) {
	s, creator := newCreatedService(t)
	events := subscribeRoom(t, s, creator.SessionToken)
	if _, err := s.Send(testContext, creator.SessionToken, "one", "hello"); err != nil {
		t.Fatal(err)
	}
	receiveEvent(t, events, "message")
	s.Close()
	s.Close()
	if event, ok := <-events; ok {
		t.Fatalf("shutdown emitted %+v", event)
	}
	if err := s.Ready(testContext); err != nil {
		t.Fatalf("Close closed pool: %v", err)
	}
	reconnected := New(s.pool, testEncryptionKey)
	ch, cancel, err := reconnected.Subscribe(testContext, creator.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if messages := receiveEvent(t, ch, "snapshot").Payload.(chat.SnapshotEvent).Room.Messages; len(messages) != 1 || messages[0].Text != "hello" {
		t.Fatalf("history after Close: %+v", messages)
	}
	if _, err := reconnected.Join(testContext, chat.JoinInput{InviteToken: creator.InviteToken}); err != nil {
		t.Fatalf("Close consumed invite: %v", err)
	}
}

func TestDatabaseErrorPreservesOnlyCallerCancellation(t *testing.T) {
	// A driver's internal timeout must not expose its connection details or be
	// mistaken for a caller deadline when the request context is still live.
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := fmt.Errorf("driver connection details: %w", cause)
		if got := databaseError(testContext, err); got != chat.ErrDatabaseUnavailable {
			t.Fatalf("internal timeout escaped application boundary: %v", got)
		}
	}
}

func TestSendAndEndRecheckSessionAfterRoomLock(t *testing.T) {
	for _, operation := range []string{"send", "end"} {
		for _, change := range []string{"revoked", "expired"} {
			t.Run(operation+"/"+change, func(t *testing.T) {
				s, creator := newCreatedService(t)
				tx, err := s.pool.Begin(testContext)
				if err != nil {
					t.Fatal(err)
				}
				defer rollback(tx)
				if _, err := tx.Exec(testContext, `SELECT id FROM rooms WHERE id = $1 FOR UPDATE`, creator.Room.ID); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(testContext, 10*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					if operation == "end" {
						done <- s.End(ctx, creator.SessionToken)
						return
					}
					_, err := s.Send(ctx, creator.SessionToken, "one", "hello")
					done <- err
				}()
				waitForBlockedTransaction(t, s.pool, tx.Conn().PgConn().PID())
				query := `UPDATE sessions SET revoked_at = clock_timestamp() WHERE room_id = $1`
				if change == "expired" {
					query = `UPDATE sessions SET expires_at = clock_timestamp() WHERE room_id = $1`
				}
				if _, err := tx.Exec(testContext, query, creator.Room.ID); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(testContext); err != nil {
					t.Fatal(err)
				}
				if err := <-done; !errors.Is(err, chat.ErrSession) {
					t.Fatalf("changed session: %v", err)
				}
			})
		}
	}
}

func TestSendAndEndLockWaitErrors(t *testing.T) {
	for _, operation := range []string{"send", "end"} {
		for _, failure := range []string{"cancel", "deadline", "database"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				s, creator := newCreatedService(t)
				tx, err := s.pool.Begin(testContext)
				if err != nil {
					t.Fatal(err)
				}
				defer rollback(tx)
				if _, err := tx.Exec(testContext, `SELECT id FROM rooms WHERE id = $1 FOR UPDATE`, creator.Room.ID); err != nil {
					t.Fatal(err)
				}
				cfg := s.pool.Config()
				if failure == "database" {
					cfg.ConnConfig.RuntimeParams["statement_timeout"] = "500ms"
				}
				pool, err := pgxpool.NewWithConfig(testContext, cfg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(pool.Close)
				writer := New(pool, testEncryptionKey)
				timeout := 5 * time.Second
				want := error(context.Canceled)
				if failure == "deadline" {
					timeout, want = 500*time.Millisecond, context.DeadlineExceeded
				}
				if failure == "database" {
					want = chat.ErrDatabaseUnavailable
				}
				ctx, cancel := context.WithTimeout(testContext, timeout)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					if operation == "end" {
						done <- writer.End(ctx, creator.SessionToken)
						return
					}
					_, err := writer.Send(ctx, creator.SessionToken, "one", "hello")
					done <- err
				}()
				waitForBlockedTransaction(t, s.pool, tx.Conn().PgConn().PID())
				if failure == "cancel" {
					cancel()
				}
				if err := <-done; err != want {
					t.Fatalf("lock failure = %v, want %v", err, want)
				}
			})
		}
	}
}
