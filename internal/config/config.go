package config

import (
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

type Config struct {
	Addr             string
	DatabaseURL      string
	IdempotencyKey   [32]byte
	MaxConns         int32
	OperationTimeout time.Duration
	ExpiryInterval   time.Duration
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:             value(getenv, "ADDR", "127.0.0.1:8080"),
		MaxConns:         8,
		OperationTimeout: 5 * time.Second,
		ExpiryInterval:   time.Second,
	}

	cfg.DatabaseURL = getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	raw, err := hex.DecodeString(getenv("IDEMPOTENCY_ENCRYPTION_KEY"))
	if err != nil || len(raw) != len(cfg.IdempotencyKey) {
		return Config{}, errors.New("IDEMPOTENCY_ENCRYPTION_KEY must be 64 hex characters")
	}
	copy(cfg.IdempotencyKey[:], raw)

	if raw := getenv("DB_MAX_CONNS"); raw != "" {
		maxConns, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || maxConns <= 0 {
			return Config{}, errors.New("DB_MAX_CONNS must be positive")
		}
		cfg.MaxConns = int32(maxConns)
	}
	if raw := getenv("DB_OPERATION_TIMEOUT"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil || duration <= 0 {
			return Config{}, errors.New("DB_OPERATION_TIMEOUT must be positive")
		}
		cfg.OperationTimeout = duration
	}
	if raw := getenv("EXPIRY_INTERVAL"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil || duration <= 0 {
			return Config{}, errors.New("EXPIRY_INTERVAL must be positive")
		}
		cfg.ExpiryInterval = duration
	}

	return cfg, nil
}

func value(getenv func(string) string, key, fallback string) string {
	if raw := getenv(key); raw != "" {
		return raw
	}
	return fallback
}
