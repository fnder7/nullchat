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

	"github.com/jackc/pgx/v5/pgxpool"

	"nullchat/internal/config"
	"nullchat/internal/httpapi"
	"nullchat/internal/storage/postgres"
	"nullchat/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err // Configuration errors contain field names, never values.
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return errors.New("invalid database configuration")
	}
	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.ConnConfig.ConnectTimeout = cfg.OperationTimeout
	startupCtx, startupCancel := context.WithTimeout(ctx, cfg.OperationTimeout)
	pool, err := pgxpool.NewWithConfig(startupCtx, poolConfig)
	if err != nil {
		startupCancel()
		return errors.New("open database pool failed")
	}
	defer pool.Close()
	err = pool.Ping(startupCtx)
	startupCancel()
	if err != nil {
		return errors.New("database unavailable at startup")
	}
	// Migrations are an explicit operator action performed by cmd/migrate, or
	// opt-in at startup (MIGRATE_ON_START) for hosts without a pre-deploy step.
	if cfg.MigrateOnStart {
		migrateCtx, migrateCancel := context.WithTimeout(ctx, time.Minute)
		err := postgres.ApplyMigrations(migrateCtx, pool)
		migrateCancel()
		if err != nil {
			return errors.New("apply migrations at startup failed")
		}
		slog.Info("database migrations applied")
	}
	service := postgres.New(pool, cfg.IdempotencyKey)
	defer service.Close() // Infrastructure only; never end rooms on shutdown.
	api := newAPI(service, cfg.OperationTimeout)
	server := &http.Server{Addr: cfg.Addr, Handler: api.Handler(web.Handler()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: serverErrorLog()}
	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	expire := func() {
		operationCtx, cancel := context.WithTimeout(workerCtx, cfg.OperationTimeout)
		defer cancel()
		if err := service.Expire(operationCtx); err != nil && workerCtx.Err() == nil {
			slog.Warn("room expiry failed; will retry", "cause", service.ExpiryFailureCause())
		}
	}
	expire() // Recover expired rooms before serving, then retry on every tick.
	expired := make(chan struct{})
	go func() {
		defer close(expired)
		ticker := time.NewTicker(cfg.ExpiryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				expire()
			}
		}
	}()
	errCh := make(chan error, 1)
	go func() { slog.Info("nullchat listening", "address", cfg.Addr); errCh <- server.ListenAndServe() }()
	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	}
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	api.Shutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown incomplete", "error", err)
		_ = server.Close()
	}
	cancelWorker()
	<-expired
	slog.Info("nullchat stopped")
	return serveErr
}

func newAPI(service httpapi.Chat, operationTimeout time.Duration) *httpapi.Server {
	return httpapi.NewWithOperationTimeout(service, operationTimeout)
}
