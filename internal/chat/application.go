package chat

import "context"

// Application is the transport-independent port used by HTTP and WebSocket adapters.
// Methods represent complete scenarios, not Get/Save primitives, so an adapter can
// keep admission, deduplication and ordering atomic.
type Application interface {
	Create(context.Context, string, int) (Credentials, error)
	Join(context.Context, string) (Credentials, error)
	Subscribe(context.Context, string) (<-chan Event, func(), error)
	Send(context.Context, string, string, string) (Message, error)
	End(context.Context, string) error
}
