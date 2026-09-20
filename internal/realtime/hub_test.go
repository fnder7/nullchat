package realtime

import "testing"

func TestHubSubscriptionLimitPerSessionKey(t *testing.T) {
	h := New()
	first, ok := h.Subscribe("room-1", "session-1", 2)
	if !ok {
		t.Fatal("first subscription was rejected")
	}
	second, ok := h.Subscribe("room-2", "session-1", 2)
	if !ok {
		t.Fatal("second subscription was rejected")
	}
	defer h.Unsubscribe(first)
	defer h.Unsubscribe(second)

	third, ok := h.Subscribe("room-3", "session-1", 2)
	if ok || third != nil {
		t.Fatal("subscription limit was not enforced per session key")
	}
}

func TestHubDisconnectsSlowSubscriberWhenQueueOverflows(t *testing.T) {
	h := New()
	sub, ok := h.Subscribe("room", "session", 1)
	if !ok {
		t.Fatal("subscription was rejected")
	}

	for i := 0; i < cap(sub.Events); i++ {
		h.Publish("room", Event{Type: "message", Payload: i})
	}
	h.Publish("room", Event{Type: "overflow"})

	count := 0
	for range sub.Events {
		count++
	}
	if count != cap(sub.Events) {
		t.Fatalf("slow subscriber retained %d events, want %d", count, cap(sub.Events))
	}
}

func TestHubCloseRoomSendsTerminalEventAndClosesChannel(t *testing.T) {
	h := New()
	sub, ok := h.Subscribe("room", "session", 1)
	if !ok {
		t.Fatal("subscription was rejected")
	}
	h.Publish("room", Event{Type: "message"})

	terminal := Event{Type: "room_closed", Payload: "terminal"}
	h.CloseRoom("room", terminal)

	got := <-sub.Events
	if got.Type != terminal.Type || got.Payload != terminal.Payload {
		t.Fatalf("got terminal event %#v, want %#v", got, terminal)
	}
	if _, ok := <-sub.Events; ok {
		t.Fatal("subscription channel is still open")
	}
}

func TestHubUnsubscribeIsIdempotent(t *testing.T) {
	h := New()
	sub, ok := h.Subscribe("room", "session", 1)
	if !ok {
		t.Fatal("subscription was rejected")
	}

	h.Unsubscribe(sub)
	h.Unsubscribe(sub)

	if _, ok := <-sub.Events; ok {
		t.Fatal("subscription channel is still open")
	}
}
