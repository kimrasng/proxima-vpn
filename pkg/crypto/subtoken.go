package crypto

import "crypto/rand"

const (
	subTokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	subTokenLength   = 48
)

// NewSubToken returns a random subscription token of uppercase letters and
// digits. Bytes at or above the largest multiple of the alphabet size are
// discarded so every character is equally likely.
func NewSubToken() string {
	const limit = 256 - 256%len(subTokenAlphabet)
	out := make([]byte, 0, subTokenLength)
	buf := make([]byte, subTokenLength)
	for len(out) < subTokenLength {
		if _, err := rand.Read(buf); err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		for _, b := range buf {
			if int(b) < limit && len(out) < subTokenLength {
				out = append(out, subTokenAlphabet[int(b)%len(subTokenAlphabet)])
			}
		}
	}
	return string(out)
}
