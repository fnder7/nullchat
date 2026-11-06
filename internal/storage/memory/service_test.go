package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nullchat/internal/chat"
	"nullchat/internal/chat/chattest"
)

var testContext = context.Background()

func create(t *testing.T, s *Service) chat.Credentials {
	t.Helper()
	c, err := s.Create(testContext, chattest.CreateInput(30))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestInviteConsumedAtomically(t *testing.T) {
	s := New()
	c := create(t, s)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Join(testContext, chattest.JoinInput(c.InviteToken)); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, chat.ErrUnavailable) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("admitted %d guests, want 1", successes.Load())
	}
	events, cancel, err := s.Subscribe(testContext, c.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if got := len((<-events).Payload.(chat.SnapshotEvent).Room.Participants); got != 2 {
		t.Fatalf("got %d participants", got)
	}
}

func TestParticipantNamesAreGeneratedAndUniquePerRoom(t *testing.T) {
	s := New()
	creator := create(t, s)
	guest, err := s.Join(testContext, chattest.JoinInput(creator.InviteToken))
	if err != nil {
		t.Fatal(err)
	}
	if creator.Self.Name == "" || guest.Self.Name == "" {
		t.Fatal("generated participant name is empty")
	}
	if creator.Self.Name == guest.Self.Name {
		t.Fatalf("generated names are not unique: %q", creator.Self.Name)
	}
	for _, name := range []string{creator.Self.Name, guest.Self.Name} {
		if _, exists := map[string]struct{}{"Fox": {}, "Owl": {}}[name]; exists {
			t.Fatalf("participant name uses a fixed legacy alias: %q", name)
		}
	}
}

func TestConcurrentDuplicateMessagesAreStoredOnce(t *testing.T) {
	s := New()
	c := create(t, s)
	body := chattest.Ciphertext(1)
	var wg sync.WaitGroup
	ids := make(chan string, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := s.Send(testContext, c.SessionToken, "client-1", body)
			if err != nil {
				t.Errorf("send: %v", err)
				return
			}
			ids <- m.ID
		}()
	}
	wg.Wait()
	close(ids)
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if first != id {
			t.Fatal("duplicate got a new ID")
		}
	}
	events, cancel, err := s.Subscribe(testContext, c.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if got := len((<-events).Payload.(chat.SnapshotEvent).Room.Messages); got != 1 {
		t.Fatalf("got %d messages", got)
	}
	if _, err := s.Send(testContext, c.SessionToken, "client-1", chattest.Ciphertext(1)); !errors.Is(err, chat.ErrConflict) {
		t.Fatalf("conflicting duplicate: %v", err)
	}
}

func TestExpirationRevokesAllAccessAtDeadline(t *testing.T) {
	s := New()
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	c := create(t, s)
	guest, err := s.Join(testContext, chattest.JoinInput(c.InviteToken))
	if err != nil {
		t.Fatal(err)
	}
	events, cancel, err := s.Subscribe(testContext, c.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	<-events
	if _, err := s.Send(testContext, c.SessionToken, "1", chattest.Ciphertext(1)); err != nil {
		t.Fatal(err)
	}
	now = c.Room.ExpiresAt
	// No background sweep is needed to enforce the deadline.
	if _, err := s.Send(testContext, guest.SessionToken, "2", chattest.Ciphertext(1)); !errors.Is(err, chat.ErrSession) {
		t.Fatalf("late send: %v", err)
	}
	if event := <-events; event.Type != "room_expired" {
		t.Fatalf("event: %s", event.Type)
	}
	if _, ok := <-events; ok {
		t.Fatal("subscription still open")
	}
	if _, _, err := s.Subscribe(testContext, c.SessionToken); !errors.Is(err, chat.ErrSession) {
		t.Fatal("expired session accepted")
	}
	if len(s.rooms)+len(s.sessions)+len(s.invites) != 0 {
		t.Fatal("expired data retained in indexes")
	}
}

func TestSnapshotAndLiveStreamHaveNoGap(t *testing.T) {
	for range 100 {
		s := New()
		c := create(t, s)
		start := make(chan struct{})
		sent := make(chan struct{})
		go func() {
			defer close(sent)
			<-start
			_, err := s.Send(testContext, c.SessionToken, "1", chattest.Ciphertext(1))
			if err != nil {
				t.Errorf("send: %v", err)
			}
		}()
		close(start)
		events, cancel, err := s.Subscribe(testContext, c.SessionToken)
		if err != nil {
			t.Fatal(err)
		}
		<-sent
		snapshot := (<-events).Payload.(chat.SnapshotEvent)
		count := len(snapshot.Room.Messages)
		select {
		case e := <-events:
			if e.Type == "message" {
				count++
			}
		default:
		}
		cancel()
		if count != 1 {
			t.Fatalf("message appears %d times across snapshot and stream", count)
		}
	}
}

func TestSlowSubscriberDoesNotBlockOthers(t *testing.T) {
	s := New()
	now := time.Now()
	s.now = func() time.Time { return now }
	c := create(t, s)
	slow, cancelSlow, err := s.Subscribe(testContext, c.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelSlow()
	fast, cancelFast, err := s.Subscribe(testContext, c.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelFast()
	<-fast
	for i := range 80 {
		now = now.Add(time.Second)
		if _, err := s.Send(testContext, c.SessionToken, fmt.Sprint(i), chattest.Ciphertext(1)); err != nil {
			t.Fatal(err)
		}
		select {
		case e := <-fast:
			if e.Type != "message" {
				t.Fatalf("got %s", e.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("fast client blocked")
		}
	}
	count := 0
	for range slow {
		count++
	}
	if count != 64 {
		t.Fatalf("slow queue retained %d events, want 64", count)
	}
	// Disconnected subscribers release their per-session connection slot.
	_, cancelNew, err := s.Subscribe(testContext, c.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	cancelNew()
}

func TestEndRevokesBothSessionsAndInvite(t *testing.T) {
	s := New()
	c := create(t, s)
	guest, err := s.Join(testContext, chattest.JoinInput(c.InviteToken))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.End(testContext, guest.SessionToken); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{c.SessionToken, guest.SessionToken} {
		if _, _, err := s.Subscribe(testContext, token); !errors.Is(err, chat.ErrSession) {
			t.Fatal("ended session accepted")
		}
	}
	if _, err := s.Join(testContext, chattest.JoinInput(c.InviteToken)); !errors.Is(err, chat.ErrUnavailable) {
		t.Fatal("ended invitation accepted")
	}
}

func TestValidationAndCapacity(t *testing.T) {
	s := New()
	for _, ttl := range []int{-1, 0, 61} {
		if _, err := s.Create(testContext, chattest.CreateInput(ttl)); !errors.Is(err, chat.ErrInput) {
			t.Fatalf("accepted TTL %d", ttl)
		}
	}
	c := create(t, s)
	for _, body := range []string{"", "hello", chattest.Ciphertext(0), chattest.Ciphertext(17), chattest.Blob(chat.NonceBytes + chat.PadBlock + chat.TagBytes + 3)} {
		if _, err := s.Send(testContext, c.SessionToken, "1", body); !errors.Is(err, chat.ErrInput) {
			t.Fatalf("accepted invalid ciphertext %q", body)
		}
	}
	for range chat.MaxRooms - 1 {
		create(t, s)
	}
	if _, err := s.Create(testContext, chattest.CreateInput(30)); !errors.Is(err, chat.ErrCapacity) {
		t.Fatal("unbounded rooms")
	}
	s.Close()
	if _, err := s.Create(testContext, chattest.CreateInput(30)); !errors.Is(err, chat.ErrCapacity) {
		t.Fatal("create after shutdown")
	}
}

func TestWrongJoinProofKeepsInviteUntilLimit(t *testing.T) {
	s := New()
	c := create(t, s)
	wrong := chattest.JoinInput(c.InviteToken)
	wrong.JoinProof = chattest.Blob(chat.MACBytes)
	for range chat.MaxJoinAttempts - 1 {
		if _, err := s.Join(testContext, wrong); !errors.Is(err, chat.ErrJoinProof) {
			t.Fatalf("wrong proof: %v", err)
		}
	}
	guest, err := s.Join(testContext, chattest.JoinInput(c.InviteToken))
	if err != nil {
		t.Fatalf("correct proof after failures: %v", err)
	}
	if c.Self.Role != chat.RoleCreator || guest.Self.Role != chat.RoleGuest || guest.Room.Participants[0].Role != chat.RoleCreator {
		t.Fatal("roles not assigned")
	}
	if guest.Room.Participants[0].KeyShare != c.Self.KeyShare {
		t.Fatal("creator key share not published to the guest")
	}
}

func TestJoinProofFailuresRevokeInvite(t *testing.T) {
	s := New()
	c := create(t, s)
	events, cancel, err := s.Subscribe(testContext, c.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	<-events
	wrong := chattest.JoinInput(c.InviteToken)
	wrong.JoinProof = chattest.Blob(chat.MACBytes)
	for range chat.MaxJoinAttempts {
		if _, err := s.Join(testContext, wrong); !errors.Is(err, chat.ErrJoinProof) {
			t.Fatalf("wrong proof: %v", err)
		}
	}
	if e := <-events; e.Type != "invite_revoked" {
		t.Fatalf("event %s, want invite_revoked", e.Type)
	}
	if _, err := s.Join(testContext, chattest.JoinInput(c.InviteToken)); !errors.Is(err, chat.ErrUnavailable) {
		t.Fatalf("revoked invite: %v", err)
	}
}

func TestInvalidJoinInputDoesNotConsumeInvite(t *testing.T) {
	s := New()
	c := create(t, s)
	bad := chattest.JoinInput(c.InviteToken)
	bad.KeyShare.MAC = ""
	if _, err := s.Join(testContext, bad); !errors.Is(err, chat.ErrInput) {
		t.Fatalf("invalid guest share: %v", err)
	}
	if _, err := s.Join(testContext, chattest.JoinInput(c.InviteToken)); err != nil {
		t.Fatalf("invalid input consumed the invite: %v", err)
	}
}

func TestCiphertextStoredVerbatim(t *testing.T) {
	s := New()
	c := create(t, s)
	body := chattest.Ciphertext(3)
	m, err := s.Send(testContext, c.SessionToken, "id-1", body)
	if err != nil || m.Ciphertext != body {
		t.Fatalf("ciphertext altered: %v", err)
	}
}
