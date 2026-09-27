package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nullchat/internal/chat"
)

type deadlineApplication struct {
	createStarted chan context.Context
}

func (a *deadlineApplication) Create(ctx context.Context, _ chat.CreateInput) (chat.Credentials, error) {
	a.createStarted <- ctx
	<-ctx.Done()
	return chat.Credentials{}, ctx.Err()
}

func (*deadlineApplication) Join(context.Context, chat.JoinInput) (chat.Credentials, error) {
	return chat.Credentials{}, chat.ErrSession
}

func (*deadlineApplication) Subscribe(context.Context, string) (<-chan chat.Event, func(), error) {
	return nil, nil, chat.ErrSession
}

func (*deadlineApplication) Send(context.Context, string, string, string) (chat.Message, error) {
	return chat.Message{}, chat.ErrSession
}

func (*deadlineApplication) End(context.Context, string) error { return chat.ErrSession }

func (*deadlineApplication) Ready(context.Context) error { return nil }

func TestRuntimeAPIUsesConfiguredOperationTimeout(t *testing.T) {
	app := &deadlineApplication{createStarted: make(chan context.Context, 1)}
	api := newAPI(app, 20*time.Millisecond)
	handler := api.Handler(http.NotFoundHandler())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/rooms", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	select {
	case operationCtx := <-app.createStarted:
		if _, ok := operationCtx.Deadline(); !ok {
			t.Fatal("runtime API did not apply DB_OPERATION_TIMEOUT")
		}
	case <-time.After(time.Second):
		t.Fatal("Create did not start")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime API did not stop the database operation")
	}
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "database_unavailable") {
		t.Fatalf("timeout response = %d %s", response.Code, response.Body.String())
	}
}
