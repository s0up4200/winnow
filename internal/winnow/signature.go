package winnow

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// validSignature reports whether h holds the HMAC-SHA256 of body under
// secret: as "sha256=<hex>" in X-Hub-Signature-256, or as "<hex>" in
// X-Forgejo-Signature. One valid header is sufficient. The compare is
// constant-time. "sha256=" with no digest is not valid.
func validSignature(secret string, body []byte, h http.Header) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sum := mac.Sum(nil)
	hub, ok := strings.CutPrefix(h.Get("X-Hub-Signature-256"), "sha256=")
	return (ok && equalHex(hub, sum)) || equalHex(h.Get("X-Forgejo-Signature"), sum)
}

// equalHex reports whether s is the hex form of sum.
func equalHex(s string, sum []byte) bool {
	got, err := hex.DecodeString(s)
	return err == nil && hmac.Equal(got, sum)
}
