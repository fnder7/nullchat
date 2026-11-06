package chat

import "encoding/base64"

// Sizes of opaque end-to-end encrypted fields. The server checks shape only;
// it never sees keys or plaintext. They mirror web/crypto.js.
const (
	NonceBytes     = 12
	TagBytes       = 16
	PadBlock       = 256
	MaxPadded      = 4096
	TitleBlobBytes = NonceBytes + PadBlock + TagBytes
	PublicKeyBytes = 65
	MACBytes       = 32
)

var b64 = base64.RawURLEncoding.Strict()

type KeyShare struct {
	PublicKey string `json:"public_key"`
	MAC       string `json:"mac"`
}

func decodeBlob(s string, max int) ([]byte, bool) {
	if len(s) > b64.EncodedLen(max) {
		return nil, false
	}
	b, err := b64.DecodeString(s)
	return b, err == nil
}

func validMessageBlob(s string) bool {
	b, ok := decodeBlob(s, NonceBytes+MaxPadded+TagBytes)
	n := len(b) - NonceBytes - TagBytes
	return ok && n >= PadBlock && n <= MaxPadded && n%PadBlock == 0
}

func validTitleBlob(s string) bool {
	b, ok := decodeBlob(s, TitleBlobBytes)
	return ok && len(b) == TitleBlobBytes
}

func validKeyShare(k KeyShare) bool {
	pub, ok := decodeBlob(k.PublicKey, PublicKeyBytes)
	return ok && len(pub) == PublicKeyBytes && pub[0] == 0x04 && valid32(k.MAC)
}

func valid32(s string) bool {
	b, ok := decodeBlob(s, MACBytes)
	return ok && len(b) == MACBytes
}
