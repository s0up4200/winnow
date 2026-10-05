package winnow

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// validSignature reports whether header is "sha256=<hex>" with the
// HMAC-SHA256 of body under secret. The compare is constant-time.
// "sha256=" with no digest is not valid.
func validSignature(secret string, body []byte, header string) bool {
	hexSum, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
