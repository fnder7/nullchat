package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nullchat/internal/chat"
	"nullchat/internal/httpapi"
	"nullchat/web"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	service := chat.New()
	api := httpapi.New(service)
	server := &http.Server{Addr: addr, Handler: api.Handler(web.Handler()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	expired := make(chan struct{})
	go func() {
		defer close(expired)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				service.Expire()
			}
		}
	}()
	errCh := make(chan error, 1)
	go func() { slog.Info("nullchat listening", "address", addr); errCh <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "error", err)
			stop()
			service.Close()
			<-expired
			os.Exit(1)
		}
	}
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	service.Close()
	api.Shutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown incomplete", "error", err)
		_ = server.Close()
	}
	<-expired
	slog.Info("nullchat stopped")
}
