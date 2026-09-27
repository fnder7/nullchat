package chat

import (
	"errors"
	"testing"
)

func TestNormalizeTitleAndMessage(t *testing.T) {
	title, err := NormalizeTitle("  Вечер  ")
	if err != nil || title != "Вечер" {
		t.Fatalf("title = %q, %v", title, err)
	}
	empty, err := NormalizeTitle("  ")
	if err != nil || empty != "Тихий разговор" {
		t.Fatalf("empty = %q, %v", empty, err)
	}
	body, err := NormalizeMessage("client-1", "  привет  ")
	if err != nil || body != "привет" {
		t.Fatalf("body = %q, %v", body, err)
	}
	if _, err := NormalizeMessage("", "hello"); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if _, err := NormalizeMessage("id", string(make([]byte, MaxTextBytes+1))); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
}

func TestNormalizeTitleRejectsNUL(t *testing.T) {
	if _, err := NormalizeTitle("before\x00after"); !errors.Is(err, ErrInput) {
		t.Fatalf("NormalizeTitle NUL error = %v, want invalid_input", err)
	}
}

func TestCreateInputKeepsIdempotencyKey(t *testing.T) {
	input := CreateInput{Title: "Room", TTLMinutes: 30, IdempotencyKey: "12345678901234567890123456789012"}
	if input.IdempotencyKey == "" {
		t.Fatal("key lost")
	}
}
