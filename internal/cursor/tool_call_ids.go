package cursor

import (
	"crypto/sha256"
	"encoding/base64"
)

func clientToolCallID(raw string) string {
	if raw == "" {
		return "call_" + uuid()
	}
	safe := len(raw) <= 64
	for i := 0; safe && i < len(raw); i++ {
		character := raw[i]
		safe = character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-'
	}
	if safe {
		return raw
	}
	digest := sha256.Sum256([]byte(raw))
	return "call_" + base64.RawURLEncoding.EncodeToString(digest[:])
}
