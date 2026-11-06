package chat

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func blob(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func testShare() KeyShare {
	pub := make([]byte, PublicKeyBytes)
	_, _ = rand.Read(pub)
	pub[0] = 0x04
	return KeyShare{PublicKey: base64.RawURLEncoding.EncodeToString(pub), MAC: blob(MACBytes)}
}

func ciphertext(blocks int) string { return blob(NonceBytes + blocks*PadBlock + TagBytes) }

func TestEnvelopeFormats(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
		got  bool
	}{
		{"message 1 block", true, validMessageBlob(ciphertext(1))},
		{"message 16 blocks", true, validMessageBlob(ciphertext(16))},
		{"message 17 blocks", false, validMessageBlob(ciphertext(17))},
		{"message 0 blocks", false, validMessageBlob(ciphertext(0))},
		{"message unaligned", false, validMessageBlob(blob(NonceBytes + PadBlock + TagBytes + 1))},
		{"message std base64", false, validMessageBlob(base64.StdEncoding.EncodeToString(make([]byte, NonceBytes+PadBlock+TagBytes)))},
		{"message huge", false, validMessageBlob(strings.Repeat("A", 1<<20))},
		{"title", true, validTitleBlob(blob(TitleBlobBytes))},
		{"title short", false, validTitleBlob(blob(TitleBlobBytes - 1))},
		{"share", true, validKeyShare(testShare())},
		{"share compressed point", false, validKeyShare(KeyShare{PublicKey: blob(33), MAC: blob(MACBytes)})},
		{"share short mac", false, validKeyShare(KeyShare{PublicKey: testShare().PublicKey, MAC: blob(16)})},
		{"secret", true, valid32(blob(32))},
		{"secret empty", false, valid32("")},
	} {
		if tc.got != tc.ok {
			t.Errorf("%s: got %v, want %v", tc.name, tc.got, tc.ok)
		}
	}
}
