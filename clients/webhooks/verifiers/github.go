package verifiers

import "github.com/theizzatbek/gokit/clients/webhooks"

// NewGitHub returns a Verifier preset for GitHub webhooks:
//
//	header=X-Hub-Signature-256, algo=SHA256, encoding=hex,
//	prefix="sha256=", no timestamp window (GitHub does not ship
//	a timestamp header).
func NewGitHub(secret []byte) webhooks.Verifier {
	v, err := NewGenericHMAC(GenericHMACConfig{
		Secret:          secret,
		SignatureHeader: "X-Hub-Signature-256",
		Algo:            HashSHA256,
		Encoding:        EncodingHex,
		Prefix:          "sha256=",
	})
	if err != nil {
		// secret == nil is the only realistic path; surface as panic
		// (programmer error, kit-startup time).
		panic(err)
	}
	return v
}
