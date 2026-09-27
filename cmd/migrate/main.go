package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"nullchat/internal/config"
	"nullchat/internal/storage/postgres"
)

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fail("load config")
	}

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		fail("open database pool")
	}
	defer pool.Close()

	if err := postgres.ApplyMigrations(context.Background(), pool); err != nil {
		fail("apply migrations")
	}
}

func fail(operation string) {
	fmt.Fprintln(os.Stderr, operation)
	os.Exit(1)
}
