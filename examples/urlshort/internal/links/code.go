// Package links holds the link domain — code generation, service, handlers.
package links

import (
	"crypto/rand"
	"math/big"
)

const codeAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
const codeLength = 6

// generateCode returns a 6-char base62 string from crypto/rand. The
// collision probability for ~10^7 stored codes is small but non-zero;
// callers retry on Postgres unique-violation up to codeRetryBudget.
func generateCode() (string, error) {
	out := make([]byte, codeLength)
	max := big.NewInt(int64(len(codeAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = codeAlphabet[n.Int64()]
	}
	return string(out), nil
}

const codeRetryBudget = 5
