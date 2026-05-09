// Package walletcrypto derives keys and seals wallet payloads using Argon2id + AES-GCM.
package walletcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	keyLen       = 32
	gcmNonceSize = 12
)

// DeriveKey combines master and runtime passwords with a per-wallet salt (Argon2id).
func DeriveKey(masterPassword, runtimePassword string, salt []byte) []byte {
	if len(salt) < 8 {
		panic("salt too short")
	}
	pass := []byte(masterPassword + "\x00" + runtimePassword)
	return argon2.IDKey(pass, salt, argonTime, argonMemory, argonThreads, keyLen)
}

// Seal encrypts plaintext with AES-256-GCM; returns nonce||ciphertext.
func Seal(plaintext, key []byte) ([]byte, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("key must be %d bytes", keyLen)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := gcm.Seal(nonce, nonce, plaintext, nil)
	return out, nil
}

// Open decrypts nonce||ciphertext produced by Seal.
func Open(key, sealed []byte) ([]byte, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("key must be %d bytes", keyLen)
	}
	if len(sealed) < gcmNonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce := sealed[:gcmNonceSize]
	ct := sealed[gcmNonceSize:]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ct, nil)
}

// RandomSalt returns n random bytes (for Argon2 salt).
func RandomSalt(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, err
	}
	return b, nil
}
