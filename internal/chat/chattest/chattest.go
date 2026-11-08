// Package chattest builds well-formed opaque E2EE fields for tests. The server
// only checks their shape, so random bytes of the right sizes are enough.
package chattest

import (
	"crypto/rand"
	"encoding/base64"

	"nullchat/internal/chat"
)

// Verifier is the join verifier used by CreateInput and accepted by JoinInput.
var Verifier = Blob(chat.MACBytes)

func Blob(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("secure random source unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func Share() chat.KeyShare {
	pub := make([]byte, chat.PublicKeyBytes)
	if _, err := rand.Read(pub); err != nil {
		panic("secure random source unavailable")
	}
	pub[0] = 0x04
	return chat.KeyShare{PublicKey: base64.RawURLEncoding.EncodeToString(pub), MAC: Blob(chat.MACBytes)}
}

// Ciphertext returns a valid message blob padded to the given number of blocks.
func Ciphertext(blocks int) string {
	return Blob(chat.NonceBytes + blocks*chat.PadBlock + chat.TagBytes)
}

func CreateInput(ttlMinutes int) chat.CreateInput {
	return chat.CreateInput{EncryptedTitle: Blob(chat.TitleBlobBytes), TTLMinutes: ttlMinutes, KeyShare: Share(), JoinVerifier: Verifier}
}

func JoinInput(invite string) chat.JoinInput {
	return chat.JoinInput{InviteToken: invite, JoinProof: Verifier, KeyShare: Share()}
}
