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

func TestServerErrorLogRedactsClientAddresses(t *testing.T) {
	var out strings.Builder
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	server.Config.ErrorLog = newErrorLog(&out)
	server.Start()
	defer server.Close()
	if response, err := http.Get(server.URL); err == nil {
		response.Body.Close()
	}
	server.Close() // flush the panic log line before reading it
	logged := out.String()
	if !strings.Contains(logged, "panic serving <addr>") {
		t.Fatalf("panic was not logged with a redacted address: %q", logged)
	}
	if strings.Contains(logged, "127.0.0.1") {
		t.Fatalf("client address leaked into the error log: %q", logged)
	}
}

func TestRedactAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"http: panic serving 203.0.113.7:51234: boom":  "http: panic serving <addr>: boom",
		"from [2001:db8::1]:443 and [fe80::1%eth0]:80": "from <addr> and <addr>",
		"plain 10.0.0.1 and version 1.27.2":            "plain <addr> and version 1.27.2",
		"keeps [conn:5] and [server:shutdown] tokens":  "keeps [conn:5] and [server:shutdown] tokens",
	} {
		if got := redactAddresses(in); got != want {
			t.Errorf("redactAddresses(%q) = %q, want %q", in, got, want)
		}
	}
}
