package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// SecretCipherPrefix marks a value produced by EncryptSecret. Anything without
// the prefix is treated as legacy plaintext, which keeps rows written before
// the cipher was introduced readable and lets the migration happen lazily on
// the next write of each row.
const SecretCipherPrefix = "enc:v1:"

var errSecretCipherEmpty = errors.New("secret cipher: no key material configured")

// secretCipherAEAD derives the AES-256-GCM AEAD from CryptoSecret. The
// derivation is salted so the same secret used elsewhere (HMAC signing) does
// not share a key stream with this cipher.
func secretCipherAEAD() (cipher.AEAD, error) {
	if strings.TrimSpace(CryptoSecret) == "" {
		return nil, errSecretCipherEmpty
	}
	key := sha256.Sum256([]byte("new-api:secret-cipher:v1:" + CryptoSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// IsEncryptedSecret reports whether the stored value already carries the
// cipher prefix.
func IsEncryptedSecret(stored string) bool {
	return strings.HasPrefix(stored, SecretCipherPrefix)
}

// EncryptSecret seals a credential for storage (version spec B03). Empty
// values and values that are already sealed pass through so callers can run it
// unconditionally on a save path.
func EncryptSecret(plain string) (string, error) {
	if plain == "" || IsEncryptedSecret(plain) {
		return plain, nil
	}
	aead, err := secretCipherAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(plain), nil)
	return SecretCipherPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// DecryptSecret opens a stored credential. Legacy plaintext rows are returned
// unchanged so a rolling upgrade never fails to read existing data.
func DecryptSecret(stored string) (string, error) {
	if !IsEncryptedSecret(stored) {
		return stored, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, SecretCipherPrefix))
	if err != nil {
		return "", err
	}
	aead, err := secretCipherAEAD()
	if err != nil {
		return "", err
	}
	nonceSize := aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("secret cipher: ciphertext is truncated")
	}
	plain, err := aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
