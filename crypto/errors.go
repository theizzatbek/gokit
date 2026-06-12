package crypto

// Stable error codes returned in [*errs.Error.Code] from the crypto
// package. Construction-time codes use KindValidation; Seal / Open
// codes use KindInternal. Downstream alerting / log-routing can match
// on these constants stably.
const (
	// CodeKeyLength — caller supplied a key of length != 32 bytes
	// (AES-256 fixed). Returned from [NewMasterKey] and
	// [NewKeychain].
	CodeKeyLength = "crypto_key_length"

	// CodeKeyBase64 — caller supplied a base64 string that does
	// not decode cleanly under any of std / URL-safe, padded / raw
	// flavours. Returned from [NewMasterKeyFromBase64] and
	// [NewKeychainFromBase64Map].
	CodeKeyBase64 = "crypto_key_base64"

	// CodeKeychainEmpty — caller passed an empty key map to
	// [NewKeychain]. A keychain with zero keys can neither seal
	// (nothing to seal with) nor open (nothing to route to), so
	// the kit rejects it at construction time.
	CodeKeychainEmpty = "crypto_keychain_empty"

	// CodeKeychainNoActive — caller's active kid is not present
	// in the supplied key map. Returned from [NewKeychain].
	CodeKeychainNoActive = "crypto_keychain_no_active"

	// CodeSealNonce — system PRNG returned an error while drawing
	// the GCM nonce. Indicates a kernel-level failure
	// (`/dev/urandom` blocked, etc.); the caller should usually
	// surface this as 503 to the upstream and not retry locally.
	CodeSealNonce = "crypto_seal_nonce"

	// CodeCiphertext — sealed blob is shorter than the header,
	// carries an unknown version byte, names a kid absent from
	// the [Keychain], or the AEAD tag verification failed (wrong
	// key, tampered). Callers must NOT distinguish these on the
	// wire — they all signal "unable to recover plaintext."
	// Surface a single generic error to upstream consumers.
	CodeCiphertext = "crypto_ciphertext"
)
