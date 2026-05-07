package signer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	lighterSigner "github.com/elliottech/lighter-go/signer"
)

// NonceProvider provides nonce values for transaction signing
type NonceProvider interface {
	NextNonce(ctx context.Context) (uint64, error)
}

// Config holds configuration for the signer
type Config struct {
	BaseURL          string
	APIKeyPrivateKey string
	AccountIndex     uint32
	APIKeyIndex      uint32
}

// Signer wraps signing functionality for Lighter API
type Signer struct {
	keyManager      lighterSigner.KeyManager
	nonceProvider   NonceProvider
	accountIndex    uint32
	apiKeyIndex     uint32
}

// New creates a new signer instance
func New(cfg Config) (*Signer, error) {
	if cfg.APIKeyPrivateKey == "" {
		return nil, fmt.Errorf("API key private key is required")
	}

	// Clean up the private key string (remove whitespace, handle 0x prefix)
	privKeyStr := cfg.APIKeyPrivateKey
	privKeyStr = strings.TrimSpace(privKeyStr)
	privKeyStr = strings.TrimPrefix(privKeyStr, "0x")
	privKeyStr = strings.TrimPrefix(privKeyStr, "0X")

	// Parse the API key private key (should be hex-encoded)
	// Lighter SDK expects 40 bytes (80 hex characters) for ECgFp5 curve
	privKeyBytes, err := hex.DecodeString(privKeyStr)
	if err != nil {
		return nil, fmt.Errorf("invalid API key private key format (not valid hex): %w", err)
	}

	// Validate key length (must be exactly 40 bytes for Lighter SDK's ECgFp5 curve)
	if len(privKeyBytes) != 40 {
		return nil, fmt.Errorf("invalid API key private key length: got %d bytes, expected 40 bytes (80 hex characters). Key length: %d chars", len(privKeyBytes), len(privKeyStr))
	}

	// Use Lighter SDK's KeyManager which handles the ECgFp5 curve and Schnorr signatures
	keyManager, err := lighterSigner.NewKeyManager(privKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to create key manager: %w", err)
	}

	return &Signer{
		keyManager:    keyManager,
		accountIndex: cfg.AccountIndex,
		apiKeyIndex:  cfg.APIKeyIndex,
	}, nil
}

// SetNonceProvider sets the nonce provider for the signer
func (s *Signer) SetNonceProvider(provider NonceProvider) {
	s.nonceProvider = provider
}

// CreateAuthTokenWithExpiry creates an authentication token with the specified expiry time
// Format: "deadline:account_index:api_key_index:signature"
func (s *Signer) CreateAuthTokenWithExpiry(ctx context.Context, expirySeconds int64) (string, error) {
	// Calculate deadline timestamp
	deadline := time.Now().Unix() + expirySeconds
	deadlineStr := strconv.FormatInt(deadline, 10)

	// Create message to sign: deadline:account_index:api_key_index
	message := fmt.Sprintf("%s:%d:%d", deadlineStr, s.accountIndex, s.apiKeyIndex)

	// Hash the message using SHA256 (Lighter SDK uses SHA256 for auth tokens)
	hasher := sha256.New()
	hasher.Write([]byte(message))
	hashedMessage32 := hasher.Sum(nil)

	// Pad the hash to 40 bytes (SDK expects exactly 40 bytes for ECgFp5 field)
	// Pad with zeros at the end (little-endian format)
	hashedMessage := make([]byte, 40)
	copy(hashedMessage, hashedMessage32)

	// Sign the hashed message using Lighter SDK's KeyManager (Schnorr signature on ECgFp5)
	signature, err := s.keyManager.Sign(hashedMessage, sha256.New())
	if err != nil {
		return "", fmt.Errorf("failed to sign message: %w", err)
	}

	// Format signature as hex (without 0x prefix for consistency with SDK format)
	signatureHex := hex.EncodeToString(signature)

	// Return token in format: "deadline:account_index:api_key_index:signature"
	token := fmt.Sprintf("%s:%d:%d:%s", deadlineStr, s.accountIndex, s.apiKeyIndex, signatureHex)
	return token, nil
}

// GetKeyManager returns the underlying KeyManager for transaction signing
func (s *Signer) GetKeyManager() lighterSigner.KeyManager {
	return s.keyManager
}

// SignerInterface for compatibility with existing code
type SignerInterface interface {
	CreateAuthTokenWithExpiry(ctx context.Context, expirySeconds int64) (string, error)
	SetNonceProvider(provider NonceProvider)
	GetKeyManager() lighterSigner.KeyManager
}
