// Package realtime owns bounded event subscriptions. It has no knowledge of
// rooms, sessions or transport protocols; the application supplies stable keys.
package realtime

import "sync"

type Event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

type Subscription struct {
	Events chan Event

	hub        *Hub
	roomKey    string
	sessionKey string
	closed     bool
}

type Hub struct {
	mu       sync.Mutex
	rooms    map[string]map[*Subscription]struct{}
	sessions map[string]map[*Subscription]struct{}
}

func New() *Hub {
	return &Hub{rooms: make(map[string]map[*Subscription]struct{}), sessions: make(map[string]map[*Subscription]struct{})}
}

func (h *Hub) Subscribe(roomKey, sessionKey string, limit int) (*Subscription, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sessions[sessionKey]) >= limit {
		return nil, false
	}
	sub := &Subscription{hub: h, roomKey: roomKey, sessionKey: sessionKey, Events: make(chan Event, 64)}
	if h.rooms[roomKey] == nil {
		h.rooms[roomKey] = make(map[*Subscription]struct{})
	}
	if h.sessions[sessionKey] == nil {
		h.sessions[sessionKey] = make(map[*Subscription]struct{})
	}
	h.rooms[roomKey][sub] = struct{}{}
	h.sessions[sessionKey][sub] = struct{}{}
	return sub, true
}

func (h *Hub) Unsubscribe(sub *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.unsubscribeLocked(sub)
}

func (h *Hub) Publish(roomKey string, event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.rooms[roomKey] {
		select {
		case sub.Events <- event:
		default:
			h.unsubscribeLocked(sub)
		}
	}
}

func (h *Hub) CloseRoom(roomKey string, event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.rooms[roomKey] {
		for {
			select {
			case <-sub.Events:
			default:
				goto drained
			}
		}
	drained:
		sub.Events <- event
		h.unsubscribeLocked(sub)
	}
}

func (h *Hub) unsubscribeLocked(sub *Subscription) {
	if sub == nil || sub.closed {
		return
	}
	sub.closed = true
	delete(h.rooms[sub.roomKey], sub)
	if len(h.rooms[sub.roomKey]) == 0 {
		delete(h.rooms, sub.roomKey)
	}
	delete(h.sessions[sub.sessionKey], sub)
	if len(h.sessions[sub.sessionKey]) == 0 {
		delete(h.sessions, sub.sessionKey)
	}
	close(sub.Events)
}
