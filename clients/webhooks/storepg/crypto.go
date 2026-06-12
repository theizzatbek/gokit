package storepg

import (
	"errors"

	"github.com/theizzatbek/gokit/clients/webhooks"
	gocrypto "github.com/theizzatbek/gokit/crypto"
	"github.com/theizzatbek/gokit/errs"
)

// crypto is a thin private wrapper over [gokit/crypto.MasterKey].
//
// The wrapper exists for two reasons:
//
//   - The webhooks store has shipped its own [webhooks.CodeStorepgNoKey] /
//     [webhooks.CodeStorepgDecryptFailed] codes since v0.x; downstream
//     alerting rules pattern-match on those strings. Re-tagging the
//     underlying [gocrypto.CodeKeyLength] / [gocrypto.CodeCiphertext]
//     here preserves the wire contract.
//   - The store calls `c.seal(...)` / `c.open(...)` against a private
//     type from many files; keeping the symbol stable avoids touching
//     half the package for what is otherwise an internal refactor.
//
// New consumers should reach for [gokit/crypto.MasterKey] directly —
// this wrapper exists for internal kit symmetry only.
type crypto struct {
	mk *gocrypto.MasterKey
}

func newCrypto(key []byte) (*crypto, error) {
	mk, err := gocrypto.NewMasterKey(key)
	if err != nil {
		return nil, translateCryptoErr(err)
	}
	return &crypto{mk: mk}, nil
}

// seal delegates to [gocrypto.MasterKey.Seal], re-tagging any error
// with the webhooks-specific Code so downstream alerting keeps
// working.
func (c *crypto) seal(plaintext []byte) ([]byte, error) {
	sealed, err := c.mk.Seal(plaintext)
	if err != nil {
		return nil, translateCryptoErr(err)
	}
	return sealed, nil
}

// open delegates to [gocrypto.MasterKey.Open] with the same code-
// translation contract as seal.
func (c *crypto) open(sealed []byte) ([]byte, error) {
	pt, err := c.mk.Open(sealed)
	if err != nil {
		return nil, translateCryptoErr(err)
	}
	return pt, nil
}

// translateCryptoErr re-tags a [gokit/crypto] *errs.Error with the
// webhooks store's pre-v1.0 code constants so existing alerting rules
// continue to match. Construction-time codes
// ([gocrypto.CodeKeyLength], [gocrypto.CodeKeyBase64]) map to
// [webhooks.CodeStorepgNoKey]; runtime codes
// ([gocrypto.CodeCiphertext], [gocrypto.CodeSealNonce]) map to
// [webhooks.CodeStorepgDecryptFailed]. Non-errs errors pass through
// unchanged.
func translateCryptoErr(err error) error {
	var e *errs.Error
	if !errors.As(err, &e) {
		return err
	}
	switch e.Code {
	case gocrypto.CodeKeyLength, gocrypto.CodeKeyBase64, gocrypto.CodeKeychainEmpty, gocrypto.CodeKeychainNoActive:
		return errs.Wrap(err, e.Kind, webhooks.CodeStorepgNoKey, e.Message)
	case gocrypto.CodeCiphertext, gocrypto.CodeSealNonce:
		return errs.Wrap(err, e.Kind, webhooks.CodeStorepgDecryptFailed, e.Message)
	default:
		return err
	}
}
