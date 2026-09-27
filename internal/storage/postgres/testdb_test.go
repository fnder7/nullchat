package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var testContext = context.Background()

func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(testContext, 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(testContext, 5*time.Second)
		defer cleanupCancel()
		defer pool.Close()

		if _, err := pool.Exec(cleanupCtx, `TRUNCATE TABLE idempotency_requests, messages, sessions, invites, participants, rooms RESTART IDENTITY CASCADE`); err != nil {
			t.Error(err)
		}
	})

	return pool
}
