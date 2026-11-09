package newsession

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// idBytes is a UUID's length in bytes.
	idBytes = 16
	// versionByte and variantByte hold a UUID's version and variant bits (RFC 9562).
	versionByte = 6
	variantByte = 8
	// versionRandom marks version 4 (random) in the version byte's high nibble.
	versionRandom = 0x40
	versionMask   = 0x0f
	// variantRFC marks the RFC 9562 variant in the variant byte's two high bits.
	variantRFC  = 0x80
	variantMask = 0x3f
)

// groupEnds are where each dash-separated group of a UUID's hex ends.
func groupEnds() []int { return []int{8, 12, 16, 20, 32} }

// NewID is a fresh conversation id: a random (version 4) UUID from crypto/rand, in the lowercase
// form claude's --session-id takes.
func NewID() (string, error) {
	raw := make([]byte, idBytes)

	_, err := rand.Read(raw)
	if err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}

	raw[versionByte] = raw[versionByte]&versionMask | versionRandom
	raw[variantByte] = raw[variantByte]&variantMask | variantRFC

	hexed := hex.EncodeToString(raw)
	groups := make([]string, 0, len(groupEnds()))
	start := 0

	for _, end := range groupEnds() {
		groups = append(groups, hexed[start:end])
		start = end
	}

	return strings.Join(groups, "-"), nil
}
