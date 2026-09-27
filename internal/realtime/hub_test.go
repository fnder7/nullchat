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

func TestHubCloseAllClosesSubscriptionsWithoutTerminalEvent(t *testing.T) {
	h := New()
	var subscriptions []*Subscription
	for _, room := range []string{"one", "two"} {
		for range 2 {
			sub, ok := h.Subscribe(room, room, 2)
			if !ok {
				t.Fatal("subscription rejected")
			}
			subscriptions = append(subscriptions, sub)
		}
		h.Publish(room, Event{Type: "message"})
	}
	h.CloseAll()
	h.CloseAll()
	for _, sub := range subscriptions {
		for event := range sub.Events {
			if event.Type != "message" {
				t.Fatalf("shutdown emitted %q", event.Type)
			}
		}
		h.Unsubscribe(sub)
	}
	if _, ok := h.Subscribe("one", "one", 2); !ok {
		t.Fatal("closed subscriptions still consume capacity")
	}
	h.CloseAll()
}
