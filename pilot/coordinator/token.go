package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func randomLeaseToken() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("read cryptographic randomness: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}
