package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL":               "postgres://nullchat:secret@localhost:5432/nullchat?sslmode=disable",
		"IDEMPOTENCY_ENCRYPTION_KEY": strings.Repeat("ab", 32),
	}
	getenv := func(key string) string { return env[key] }

	cfg, err := Load(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:8080" || cfg.MaxConns != 8 || cfg.OperationTimeout != 5*time.Second || cfg.ExpiryInterval != time.Second {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.DatabaseURL != env["DATABASE_URL"] {
		t.Fatalf("database URL = %q", cfg.DatabaseURL)
	}

	env["IDEMPOTENCY_ENCRYPTION_KEY"] = "bad"
	if _, err := Load(getenv); err == nil {
		t.Fatal("accepted short key")
	}
	delete(env, "DATABASE_URL")
	if _, err := Load(getenv); err == nil {
		t.Fatal("accepted missing database URL")
	}
}

func TestLoadParsesOptionalValues(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL":               "postgres://localhost/nullchat",
		"IDEMPOTENCY_ENCRYPTION_KEY": strings.Repeat("00", 32),
		"ADDR":                       "0.0.0.0:9000",
		"DB_MAX_CONNS":               "12",
		"DB_OPERATION_TIMEOUT":       "750ms",
		"EXPIRY_INTERVAL":            "2s",
	}
	cfg, err := Load(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "0.0.0.0:9000" || cfg.MaxConns != 12 || cfg.OperationTimeout != 750*time.Millisecond || cfg.ExpiryInterval != 2*time.Second {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestLoadRejectsInvalidOptionalValues(t *testing.T) {
	for _, key := range []string{"DB_MAX_CONNS", "DB_OPERATION_TIMEOUT", "EXPIRY_INTERVAL"} {
		t.Run(key, func(t *testing.T) {
			env := map[string]string{
				"DATABASE_URL":               "postgres://localhost/nullchat",
				"IDEMPOTENCY_ENCRYPTION_KEY": strings.Repeat("00", 32),
				key:                          "0",
			}
			if _, err := Load(func(name string) string { return env[name] }); err == nil {
				t.Fatalf("accepted non-positive %s", key)
			}
		})
	}
}

func TestLoadDoesNotExposeSecretsInErrors(t *testing.T) {
	secretURL := "postgres://user:secret@localhost/nullchat"
	secretKey := strings.Repeat("ab", 31) + "zz"
	env := map[string]string{
		"DATABASE_URL":               secretURL,
		"IDEMPOTENCY_ENCRYPTION_KEY": secretKey,
	}
	_, err := Load(func(key string) string { return env[key] })
	if err == nil {
		t.Fatal("accepted invalid key")
	}
	if strings.Contains(err.Error(), secretURL) || strings.Contains(err.Error(), secretKey) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error exposed a secret: %v", err)
	}
}
