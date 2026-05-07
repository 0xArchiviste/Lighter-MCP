package account

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

// AccountInfo represents basic account information
type AccountInfo struct {
	Index           uint32  `json:"index"`
	AccountIndex    uint32  `json:"account_index"`
	L1Address      string  `json:"l1_address"`
	AvailableBalance string `json:"available_balance"`
	TotalAssetValue string  `json:"total_asset_value"`
	Collateral     string  `json:"collateral"`
}

// DeriveL1AddressFromPrivateKey derives the Ethereum address from a private key
func DeriveL1AddressFromPrivateKey(privKeyHex string) (string, error) {
	// Clean up the private key string
	privKeyStr := strings.TrimSpace(privKeyHex)
	privKeyStr = strings.TrimPrefix(privKeyStr, "0x")
	privKeyStr = strings.TrimPrefix(privKeyStr, "0X")

	// Parse the private key (should be 32 bytes = 64 hex chars for Ethereum)
	privKeyBytes, err := hex.DecodeString(privKeyStr)
	if err != nil {
		return "", fmt.Errorf("invalid private key format: %w", err)
	}

	if len(privKeyBytes) != 32 {
		return "", fmt.Errorf("invalid private key length: got %d bytes, expected 32 bytes (64 hex characters)", len(privKeyBytes))
	}

	privKey, err := crypto.ToECDSA(privKeyBytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	// Derive public key and address
	publicKey := privKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("failed to cast public key to ECDSA")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA)
	return address.Hex(), nil
}

// ListAccounts lists all accounts for an L1 address
func (c *Client) ListAccounts(ctx context.Context, l1Address string, authToken string) ([]AccountInfo, error) {
	// Try to fetch account by L1 address first (this might return the account)
	// If the accounts_by_l1_address endpoint doesn't work, we'll query by L1 address
	rawData, err := c.apiClient.AccountsByL1Address(ctx, l1Address, authToken)
	if err != nil {
		// Fallback: try querying account by L1 address directly
		rawData, err = c.apiClient.AccountData(ctx, l1Address, nil, authToken)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch accounts: %w", err)
		}
		// If we got data from AccountData, it's a single account response
		// Parse it as a single account in an array
		var accountMap map[string]interface{}
		if err := json.Unmarshal(rawData, &accountMap); err == nil {
			// Check if it's the accounts array format
			if accountsArray, ok := accountMap["accounts"].([]interface{}); ok {
				rawData, _ = json.Marshal(map[string]interface{}{
					"accounts": accountsArray,
				})
			} else {
				// Single account, wrap in array
				rawData, _ = json.Marshal(map[string]interface{}{
					"accounts": []interface{}{accountMap},
				})
			}
		}
	}

	// Parse response
	var responseMap map[string]interface{}
	if err := json.Unmarshal(rawData, &responseMap); err != nil {
		return nil, fmt.Errorf("failed to parse accounts data: %w", err)
	}

	var accounts []AccountInfo

	// Handle different response formats
	if accountsArray, ok := responseMap["accounts"].([]interface{}); ok {
		for _, acc := range accountsArray {
			if accMap, ok := acc.(map[string]interface{}); ok {
				var info AccountInfo
				
				// Extract index
				if idx, ok := accMap["index"].(float64); ok {
					info.Index = uint32(idx)
					info.AccountIndex = uint32(idx)
				} else if idx, ok := accMap["account_index"].(float64); ok {
					info.AccountIndex = uint32(idx)
					info.Index = uint32(idx)
				}

				// Extract L1 address
				if addr, ok := accMap["l1_address"].(string); ok {
					info.L1Address = addr
				}

				// Extract balances
				if available, ok := accMap["available_balance"].(string); ok {
					info.AvailableBalance = available
				}
				if total, ok := accMap["total_asset_value"].(string); ok {
					info.TotalAssetValue = total
				}
				if collateral, ok := accMap["collateral"].(string); ok {
					info.Collateral = collateral
				}

				accounts = append(accounts, info)
			}
		}
	}

	return accounts, nil
}

