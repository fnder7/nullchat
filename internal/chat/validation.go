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

func NormalizeTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if strings.IndexByte(title, 0) >= 0 || !utf8.ValidString(title) || utf8.RuneCountInString(title) > 60 {
		return "", ErrInput
	}
	if title == "" {
		return "Тихий разговор", nil
	}
	return title, nil
}

func NormalizeMessage(clientID, body string) (string, error) {
	body = strings.TrimSpace(body)
	if clientID == "" || !utf8.ValidString(clientID) || len(clientID) > 64 || !utf8.ValidString(body) || body == "" || len(body) > MaxTextBytes {
		return "", ErrInput
	}
	return body, nil
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
