package credential

import (
	"crypto/sha256"
	"encoding/hex"
)

func fingerprintDER(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}
