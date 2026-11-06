package chat

import (
	"crypto/rand"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

var generatedNames = []string{
	"Amber", "Aspen", "Atlas", "Basil", "Birch", "Brook", "Cedar", "Clover",
	"Comet", "Coral", "Dawn", "Echo", "Ember", "Fern", "Flint", "Glade",
	"Harbor", "Hazel", "Indigo", "Iris", "Jasper", "Juniper", "Lumen", "Maple",
	"Mica", "Moss", "Nova", "Olive", "Onyx", "Orchid", "River", "Robin",
	"Sable", "Sage", "Solstice", "Stone", "Summit", "Terra", "Willow", "Zephyr",
}

// ValidateCreate checks the shape of a create request. Title and keys are
// encrypted or opaque; their content is the browsers' business.
func ValidateCreate(input CreateInput) error {
	if input.TTLMinutes < 1 || input.TTLMinutes > 60 || !validTitleBlob(input.EncryptedTitle) || !validKeyShare(input.KeyShare) || !valid32(input.JoinVerifier) {
		return ErrInput
	}
	return nil
}

func ValidateJoin(input JoinInput) error {
	if !valid32(input.JoinProof) || !validKeyShare(input.KeyShare) {
		return ErrInput
	}
	return nil
}

// ValidateMessage checks a client message ID and an opaque padded ciphertext.
func ValidateMessage(clientID, ciphertext string) error {
	if clientID == "" || !utf8.ValidString(clientID) || len(clientID) > 64 || strings.IndexByte(clientID, 0) >= 0 || !validMessageBlob(ciphertext) {
		return ErrInput
	}
	return nil
}

func GenerateParticipantName(existing []Participant) (string, error) {
	used := make(map[string]struct{}, len(existing))
	for _, participant := range existing {
		used[participant.Name] = struct{}{}
	}
	for range len(generatedNames) * 2 {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(generatedNames))))
		if err != nil {
			return "", err
		}
		name := generatedNames[index.Int64()]
		if _, exists := used[name]; !exists {
			return name, nil
		}
	}
	return "Guest " + strconv.Itoa(len(existing)+1), nil
}
