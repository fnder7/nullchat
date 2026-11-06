package config

import (
	"encoding/base64"
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
	// MigrateOnStart applies embedded migrations before serving. Off by default:
	// migrations stay an explicit operator action unless the host has no
	// pre-deploy step (for example Render's free tier).
	MigrateOnStart bool
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:             listenAddr(getenv),
		MaxConns:         8,
		OperationTimeout: 5 * time.Second,
		ExpiryInterval:   time.Second,
	}

	cfg.DatabaseURL = getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	raw, ok := decodeKey(getenv("IDEMPOTENCY_ENCRYPTION_KEY"))
	if !ok {
		return Config{}, errors.New("IDEMPOTENCY_ENCRYPTION_KEY must be 32 bytes as 64 hex characters or base64")
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

	switch getenv("MIGRATE_ON_START") {
	case "", "false", "0":
	case "true", "1":
		cfg.MigrateOnStart = true
	default:
		return Config{}, errors.New("MIGRATE_ON_START must be true or false")
	}

	return cfg, nil
}

// decodeKey accepts 32 bytes as hex (openssl rand -hex 32) or standard base64,
// which is what Render's generateValue produces.
func decodeKey(value string) ([]byte, bool) {
	if raw, err := hex.DecodeString(value); err == nil && len(raw) == 32 {
		return raw, true
	}
	if raw, err := base64.StdEncoding.DecodeString(value); err == nil && len(raw) == 32 {
		return raw, true
	}
	return nil, false
}

// listenAddr prefers an explicit ADDR. Platforms such as Render announce the
// port through PORT and expect the service on all interfaces.
func listenAddr(getenv func(string) string) string {
	if addr := getenv("ADDR"); addr != "" {
		return addr
	}
	if port := getenv("PORT"); port != "" {
		return "0.0.0.0:" + port
	}
	return "127.0.0.1:8080"
}
