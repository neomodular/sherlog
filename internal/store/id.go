package store

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// idLength is the number of base36 characters in a generated session ID. The
// spec requires ≥8 random base36 characters; the unguessable path segment is the
// only thing stopping drive-by localhost log injection (D4).
const idLength = 8

const base36 = "0123456789abcdefghijklmnopqrstuvwxyz"

// newID returns a cryptographically random base36 token of idLength characters.
// crypto/rand (not math/rand) is mandatory: the token doubles as an access
// capability for the session's log endpoint (D4).
func newID() (string, error) {
	max := big.NewInt(int64(len(base36)))
	buf := make([]byte, idLength)
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generate session id: %w", err)
		}
		buf[i] = base36[n.Int64()]
	}
	return string(buf), nil
}
