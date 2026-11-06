package chat

import (
	"errors"
	"testing"
)

func TestValidateCreateJoinAndMessage(t *testing.T) {
	create := CreateInput{EncryptedTitle: blob(TitleBlobBytes), TTLMinutes: 30, KeyShare: testShare(), JoinVerifier: blob(MACBytes)}
	if err := ValidateCreate(create); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CreateInput){
		"ttl":      func(in *CreateInput) { in.TTLMinutes = 61 },
		"title":    func(in *CreateInput) { in.EncryptedTitle = "plain title" },
		"share":    func(in *CreateInput) { in.KeyShare.MAC = "" },
		"verifier": func(in *CreateInput) { in.JoinVerifier = blob(8) },
	} {
		in := create
		mutate(&in)
		if err := ValidateCreate(in); !errors.Is(err, ErrInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := ValidateJoin(JoinInput{JoinProof: blob(MACBytes), KeyShare: testShare()}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateJoin(JoinInput{JoinProof: blob(MACBytes), KeyShare: KeyShare{PublicKey: "A" + blob(PublicKeyBytes)[1:], MAC: blob(MACBytes)}}); !errors.Is(err, ErrInput) {
		t.Fatal("accepted a compressed/invalid public key")
	}
	if err := ValidateMessage("client-1", ciphertext(1)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, body string }{{"", ciphertext(1)}, {"id\x00", ciphertext(1)}, {"id", "hello"}, {"id", ciphertext(17)}} {
		if err := ValidateMessage(tc.id, tc.body); !errors.Is(err, ErrInput) {
			t.Errorf("accepted %q", tc.id)
		}
	}
}

func TestCreateInputKeepsIdempotencyKey(t *testing.T) {
	input := CreateInput{TTLMinutes: 30, IdempotencyKey: "12345678901234567890123456789012"}
	if input.IdempotencyKey == "" {
		t.Fatal("key lost")
	}
}
