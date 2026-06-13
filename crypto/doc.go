// Package crypto is the kit's standard at-rest sealing primitive.
//
// Two API surfaces:
//
//   - [MasterKey] — single AES-256-GCM key for the static case
//     ("encrypt every refresh token before it touches Postgres").
//   - [Keychain] — kid-routed multi-key sealing for the rotation case
//     ("decrypt old blobs with key v1, encrypt new ones with key v2").
//
// Both write self-contained byte blobs that carry a version byte
// (and a kid byte for Keychain) so a single Open call routes
// unambiguously without out-of-band metadata. Wire format:
//
//	MasterKey:  [version=0x01] [nonce(12)] [ciphertext+tag(N+16)]
//	Keychain:   [version=0x02] [kid(1)]     [nonce(12)] [ciphertext+tag(N+16)]
//
// MasterKey.Open rejects any blob whose version is not 0x01;
// Keychain.Open rejects any blob whose version is not 0x02. Future
// ciphersuites (post-quantum AEADs etc.) get 0x03 / 0x04 with a
// matching constructor — Open paths stay format-isolated.
//
// # Key-material conventions
//
// Both constructors take exactly 32 raw bytes. The base64 convenience
// constructors ([NewMasterKeyFromBase64], [NewKeychainFromBase64Map])
// accept every Go stdlib flavour — standard / URL-safe, padded / raw —
// so operators copy-pasting from arbitrary key-management UIs see
// "just works" instead of cryptic decoder errors.
//
// # Failure modes
//
// Construction-time errors are [*errs.Error] of Kind = Validation:
//   - [CodeKeyLength]     — key bytes not exactly 32.
//   - [CodeKeyBase64]     — base64 string decoded clean under no flavour.
//   - [CodeKeychainEmpty] — Keychain initialised with zero keys.
//   - [CodeKeychainNoActive] — Keychain's active kid not in its key map.
//
// Seal / Open errors are [*errs.Error] of Kind = Internal:
//   - [CodeSealNonce]   — system PRNG returned an error reading nonce.
//   - [CodeCiphertext]  — sealed blob is shorter than the header,
//     carries an unknown version byte, names a kid
//     absent from the Keychain, or the AEAD tag
//     verification failed (wrong key, tampered).
//     Callers should not distinguish these on the
//     wire — they all signal "unable to recover
//     plaintext."
//
// errors.Is + the Code constants let callers branch when needed:
//
//	_, err := mk.Open(sealed)
//	if err != nil {
//	    var e *errs.Error
//	    if errors.As(err, &e) && e.Code == crypto.CodeCiphertext {
//	        // bad blob — log + drop the row.
//	    }
//	}
//
// # Goroutine safety
//
// MasterKey and Keychain values are safe for concurrent use after
// construction. The underlying cipher.AEAD is goroutine-safe per the
// stdlib contract, and neither type holds mutable state.
package crypto
