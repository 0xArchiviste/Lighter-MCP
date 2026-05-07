package account

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"time"

	lighterclient "github.com/elliottech/lighter-go/client"
	lighterSigner "github.com/elliottech/lighter-go/signer"
	lightertypes "github.com/elliottech/lighter-go/types"
	lightertxtypes "github.com/elliottech/lighter-go/types/txtypes"
	"github.com/0xarchiviste/lighter-mcp/pkg/api"
	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/markets"
)

// MarginMode represents the margin mode
// Note: Cross margin (0) should generally be avoided; use Isolated margin (1) instead
type MarginMode uint8

const (
	CrossMargin    MarginMode = 0 // Cross margin - NOT RECOMMENDED
	IsolatedMargin MarginMode = 1 // Isolated margin - RECOMMENDED
)

// MarginDirection represents the direction for margin amount changes
type MarginDirection uint8

const (
	RemoveFromIsolatedMargin MarginDirection = 0
	AddToIsolatedMargin      MarginDirection = 1
)

// LeverageToInitialMarginFraction converts leverage (e.g., 1x, 10x, 50x) to initial margin fraction
// Formula: initialMarginFraction = 10000 / leverage
// Examples:
//   - 1x leverage = 10000 / 1 = 10000
//   - 2x leverage = 10000 / 2 = 5000
//   - 3x leverage = 10000 / 3 = 3333
//   - 10x leverage = 10000 / 10 = 1000
//   - 50x leverage = 10000 / 50 = 200
func LeverageToInitialMarginFraction(leverage uint16) uint16 {
	if leverage == 0 {
		return 0
	}
	return 10000 / leverage
}

// MarginClient provides margin-related operations
type MarginClient struct {
	apiClient  *api.Client
	cfg        config.Config
	txClient   *lighterclient.TxClient
	keyManager lighterSigner.KeyManager
}

// NewMarginClient creates a new margin client
// If accountIndex is 0 and ethPrivateKey is provided, it will auto-discover the account index
func NewMarginClient(apiClient *api.Client, cfg config.Config, keyManager lighterSigner.KeyManager, accountIndex *uint32) (*MarginClient, error) {
	// Create SDK HTTP client wrapper
	httpClient := apiClient.GetSDKClient()
	if httpClient == nil {
		return nil, fmt.Errorf("SDK HTTP client not available")
	}

	// Determine the account index to use
	var finalAccountIndex uint32
	if accountIndex != nil && *accountIndex > 0 {
		finalAccountIndex = *accountIndex
	} else {
		finalAccountIndex = cfg.AccountIndex
	}

	// Validate account index
	if finalAccountIndex == 0 {
		return nil, fmt.Errorf("invalid account index: 0. Please set LIGHTER_ACCOUNT_INDEX or use account discovery")
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Creating TxClient with account_index=%d, api_key_index=%d\n", finalAccountIndex, cfg.APIKeyIndex)

	// Create SDK transaction client
	// NewTxClient(apiClient MinimalHTTPClient, apiKeyPrivateKey string, accountIndex int64, apiKeyIndex uint8, chainId uint32)
	txClient, err := lighterclient.NewTxClient(
		httpClient,
		cfg.APIKeyPrivateKey,
		int64(finalAccountIndex),
		uint8(cfg.APIKeyIndex),
		cfg.ChainID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create transaction client: %w", err)
	}

	// Update cfg with the final account index
	cfg.AccountIndex = finalAccountIndex

	return &MarginClient{
		apiClient:  apiClient,
		cfg:        cfg,
		txClient:   txClient,
		keyManager: keyManager,
	}, nil
}

// SetMarginType changes the margin type (isolated/cross) for a market
// marketSymbol: Market symbol (e.g., "ETH", "BTC")
// marginMode: CrossMargin (0) or IsolatedMargin (1)
//
//	WARNING: Cross margin (0) is NOT RECOMMENDED. Use IsolatedMargin (1) instead.
//
// initialMarginFraction: Initial margin fraction (leverage setting)
//
//	Formula: initialMarginFraction = 10000 / leverage
//	Examples: 1x=10000, 2x=5000, 3x=3333, 10x=1000, 50x=200
//	Use LeverageToInitialMarginFraction() to convert leverage multiplier to initial margin fraction
//
// authToken: Authentication token for API calls
func (c *MarginClient) SetMarginType(ctx context.Context, marketSymbol string, marginMode MarginMode, initialMarginFraction uint16, authToken string) (string, error) {
	// Warn if trying to use cross margin
	if marginMode == CrossMargin {
		return "", fmt.Errorf("cross margin (0) is not recommended and should be avoided. Use isolated margin (1) instead")
	}
	// Resolve market symbol to market ID using markets.json
	marketID, err := markets.GetMarketIDGlobal(marketSymbol)
	if err != nil {
		return "", fmt.Errorf("failed to resolve market ID: %w", err)
	}

	// Convert market_id (uint32) to market_index (uint8)
	// They're the same for values <= 255
	if marketID > 255 {
		return "", fmt.Errorf("market_id %d exceeds uint8 range (0-255)", marketID)
	}
	marketIndex := uint8(marketID)

	// Get next nonce FIRST using REST API with auth token
	// Use the account_index and api_key_index from config (matches curl example format)
	accountIndex := c.cfg.AccountIndex
	apiKeyIndex := c.cfg.APIKeyIndex

	fmt.Fprintf(os.Stderr, "[DEBUG] Getting nonce for account_index=%d, api_key_index=%d (from config, matching curl)\n", accountIndex, apiKeyIndex)

	// Always use REST API with auth token for nonce - SDK's GetNextNonce might not work correctly
	nonce, err := c.apiClient.NextNonceWithAuth(ctx, authToken)
	if err != nil {
		return "", fmt.Errorf("failed to get nonce: %w", err)
	}
	nonceInt64 := int64(nonce)
	fmt.Fprintf(os.Stderr, "[DEBUG] Got nonce from REST API: %d\n", nonceInt64)

	// Validate nonce is reasonable (should be > 0)
	if nonce == 0 {
		return "", fmt.Errorf("invalid nonce: 0 (account might not exist or API key is invalid)")
	}

	// Create transaction request
	txReq := &lightertypes.UpdateLeverageTxReq{
		MarketIndex:           marketIndex,
		InitialMarginFraction: initialMarginFraction,
		MarginMode:            uint8(marginMode),
	}

	// Create transaction options WITH THE CORRECT NONCE
	// ExpiredAt must be in milliseconds (not seconds!)
	expiredAt := time.Now().Add(1 * time.Hour).UnixMilli()
	accountIndexForOps := int64(c.cfg.AccountIndex)
	apiKeyIndexForOps := uint8(c.cfg.APIKeyIndex)
	nonceInt64ForOps := nonceInt64 // Use the nonce we just fetched

	ops := &lightertypes.TransactOpts{
		FromAccountIndex: &accountIndexForOps,
		ApiKeyIndex:      &apiKeyIndexForOps,
		ExpiredAt:        expiredAt,
		Nonce:            &nonceInt64ForOps,
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Creating transaction with nonce=%d (will be signed with this nonce)\n", nonceInt64ForOps)

	// CRITICAL: Call FullFillDefaultOps to ensure the SDK uses our nonce
	// This might fetch its own nonce if we don't do this first
	filledOps, err := c.txClient.FullFillDefaultOps(ops)
	if err != nil {
		return "", fmt.Errorf("failed to fill default ops: %w", err)
	}

	// Ensure the nonce in filledOps matches what we fetched
	if filledOps.Nonce != nil && *filledOps.Nonce != nonceInt64ForOps {
		fmt.Fprintf(os.Stderr, "[DEBUG] FullFillDefaultOps changed nonce from %d to %d, overriding...\n", nonceInt64ForOps, *filledOps.Nonce)
		filledOps.Nonce = &nonceInt64ForOps
	}

	// Build transaction - SDK will sign it with the nonce we provided
	leverageTx, err := c.txClient.GetUpdateLeverageTransaction(txReq, filledOps)
	if err != nil {
		return "", fmt.Errorf("failed to build transaction: %w", err)
	}

	// CRITICAL: Re-sign the transaction with the correct nonce to ensure signature matches
	// The SDK might have signed with a different nonce, so we regenerate the signature
	// Ensure the nonce matches what we fetched
	if leverageTx.Nonce != nonceInt64ForOps {
		fmt.Fprintf(os.Stderr, "[DEBUG] Nonce mismatch! SDK used %d, we want %d. Re-signing...\n", leverageTx.Nonce, nonceInt64ForOps)
		leverageTx.Nonce = nonceInt64ForOps
	}

	// Always re-sign the transaction with the correct nonce to ensure signature matches
	// Use the SDK's TxClient's KeyManager to sign (same one used by SDK)
	txClientKeyManager := c.txClient.GetKeyManager()

	// Hash the transaction (this includes the nonce)
	msgHash, err := leverageTx.Hash(c.cfg.ChainID)
	if err != nil {
		return "", fmt.Errorf("failed to hash transaction: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Transaction hash (for signing): %x\n", msgHash)

	// Sign the hash with the SDK's KeyManager (same one used by TxClient)
	sig, err := txClientKeyManager.Sign(msgHash, sha256.New())
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Generated signature: %x (length: %d)\n", sig, len(sig))

	// Update the signature
	leverageTx.Sig = sig
	fmt.Fprintf(os.Stderr, "[DEBUG] Re-signed transaction with nonce=%d\n", leverageTx.Nonce)

	// Serialize transaction and send
	return c.sendTransaction(ctx, leverageTx, authToken)
}

// SetMarginAmount changes the margin amount (add/remove) for isolated margin
// marketSymbol: Market symbol (e.g., "ETH", "BTC")
// usdcAmount: Amount in USDC (will be converted to micro-USDC, so 1 USDC = 1,000,000)
// direction: AddToIsolatedMargin (1) or RemoveFromIsolatedMargin (0)
// authToken: Authentication token for API calls
func (c *MarginClient) SetMarginAmount(ctx context.Context, marketSymbol string, usdcAmount *big.Int, direction MarginDirection, authToken string) (string, error) {
	// Resolve market symbol to market ID using markets.json
	marketID, err := markets.GetMarketIDGlobal(marketSymbol)
	if err != nil {
		return "", fmt.Errorf("failed to resolve market ID: %w", err)
	}

	// Convert market_id (uint32) to market_index (uint8)
	if marketID > 255 {
		return "", fmt.Errorf("market_id %d exceeds uint8 range (0-255)", marketID)
	}
	marketIndex := uint8(marketID)

	// Convert USDC amount to micro-USDC (int64)
	// USDC amounts are stored in micro-USDC (1 USDC = 1,000,000 micro-USDC)
	usdcMicro := new(big.Int).Mul(usdcAmount, big.NewInt(1_000_000))
	if !usdcMicro.IsInt64() {
		return "", fmt.Errorf("USDC amount too large: %s", usdcAmount.String())
	}
	usdcAmountInt64 := usdcMicro.Int64()

	// Get next nonce FIRST using REST API with auth token
	// Use the account_index and api_key_index from config (matches curl example format)
	accountIndex := c.cfg.AccountIndex
	apiKeyIndex := c.cfg.APIKeyIndex

	fmt.Fprintf(os.Stderr, "[DEBUG] Getting nonce for account_index=%d, api_key_index=%d (from config, matching curl)\n", accountIndex, apiKeyIndex)

	// Always use REST API with auth token for nonce - SDK's GetNextNonce might not work correctly
	nonce, err := c.apiClient.NextNonceWithAuth(ctx, authToken)
	if err != nil {
		return "", fmt.Errorf("failed to get nonce: %w", err)
	}
	nonceInt64 := int64(nonce)
	fmt.Fprintf(os.Stderr, "[DEBUG] Got nonce from REST API: %d\n", nonceInt64)

	// Validate nonce is reasonable (should be > 0)
	if nonce == 0 {
		return "", fmt.Errorf("invalid nonce: 0 (account might not exist or API key is invalid)")
	}

	// Create transaction request
	txReq := &lightertypes.UpdateMarginTxReq{
		MarketIndex: marketIndex,
		USDCAmount:  usdcAmountInt64,
		Direction:   uint8(direction),
	}

	// Create transaction options WITH THE CORRECT NONCE
	// ExpiredAt must be in milliseconds (not seconds!)
	expiredAt := time.Now().Add(1 * time.Hour).UnixMilli()
	accountIndexForOps := int64(c.cfg.AccountIndex)
	apiKeyIndexForOps := uint8(c.cfg.APIKeyIndex)
	nonceInt64ForOps := nonceInt64 // Use the nonce we just fetched

	ops := &lightertypes.TransactOpts{
		FromAccountIndex: &accountIndexForOps,
		ApiKeyIndex:      &apiKeyIndexForOps,
		ExpiredAt:        expiredAt,
		Nonce:            &nonceInt64ForOps,
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Creating transaction with nonce=%d (will be signed with this nonce)\n", nonceInt64ForOps)

	// CRITICAL: Call FullFillDefaultOps to ensure the SDK uses our nonce
	// This might fetch its own nonce if we don't do this first
	filledOps, err := c.txClient.FullFillDefaultOps(ops)
	if err != nil {
		return "", fmt.Errorf("failed to fill default ops: %w", err)
	}

	// Ensure the nonce in filledOps matches what we fetched
	if filledOps.Nonce != nil && *filledOps.Nonce != nonceInt64ForOps {
		fmt.Fprintf(os.Stderr, "[DEBUG] FullFillDefaultOps changed nonce from %d to %d, overriding...\n", nonceInt64ForOps, *filledOps.Nonce)
		filledOps.Nonce = &nonceInt64ForOps
	}

	// Build transaction - SDK will sign it with the nonce we provided
	marginTx, err := c.txClient.GetUpdateMarginTransaction(txReq, filledOps)
	if err != nil {
		return "", fmt.Errorf("failed to build transaction: %w", err)
	}

	// CRITICAL: Re-sign the transaction with the correct nonce to ensure signature matches
	// The SDK might have signed with a different nonce, so we regenerate the signature
	// Ensure the nonce matches what we fetched
	if marginTx.Nonce != nonceInt64ForOps {
		fmt.Fprintf(os.Stderr, "[DEBUG] Nonce mismatch! SDK used %d, we want %d. Re-signing...\n", marginTx.Nonce, nonceInt64ForOps)
		marginTx.Nonce = nonceInt64ForOps
	}

	// Always re-sign the transaction with the correct nonce to ensure signature matches
	// Use the SDK's TxClient's KeyManager to sign (same one used by SDK)
	txClientKeyManager := c.txClient.GetKeyManager()

	// Hash the transaction (this includes the nonce)
	msgHash, err := marginTx.Hash(c.cfg.ChainID)
	if err != nil {
		return "", fmt.Errorf("failed to hash transaction: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Transaction hash (for signing): %x\n", msgHash)

	// Sign the hash with the SDK's KeyManager (same one used by TxClient)
	sig, err := txClientKeyManager.Sign(msgHash, sha256.New())
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Generated signature: %x (length: %d)\n", sig, len(sig))

	// Update the signature
	marginTx.Sig = sig
	fmt.Fprintf(os.Stderr, "[DEBUG] Re-signed transaction with nonce=%d\n", marginTx.Nonce)

	// Serialize transaction and send
	return c.sendTransaction(ctx, marginTx, authToken)
}

// sendTransaction serializes and sends a transaction
func (c *MarginClient) sendTransaction(ctx context.Context, txInfo lightertxtypes.TxInfo, authToken string) (string, error) {
	// Get transaction info as JSON string
	// The SDK's GetTxInfo() should already include the signature in the correct format
	txInfoJSON, err := txInfo.GetTxInfo()
	if err != nil {
		return "", fmt.Errorf("failed to serialize transaction: %w", err)
	}

	// Debug: log what GetTxInfo() returns
	fmt.Fprintf(os.Stderr, "[DEBUG] GetTxInfo() returned: %s\n", txInfoJSON)

	// Parse to check if Sig is already in the correct format
	var txPayload map[string]interface{}
	if err := json.Unmarshal([]byte(txInfoJSON), &txPayload); err != nil {
		return "", fmt.Errorf("failed to parse transaction JSON: %w", err)
	}

	// Debug: log what Sig field looks like in GetTxInfo() output
	if existingSig, ok := txPayload["Sig"]; ok {
		fmt.Fprintf(os.Stderr, "[DEBUG] Existing Sig in GetTxInfo(): type=%T, value=%v\n", existingSig, existingSig)

		// Check if Sig is already base64 (string) or needs conversion
		if sigStr, ok := existingSig.(string); ok {
			// Already a string - might be hex or base64
			previewLen := 50
			if len(sigStr) < previewLen {
				previewLen = len(sigStr)
			}
			fmt.Fprintf(os.Stderr, "[DEBUG] Sig is already a string: %s (length: %d)\n", sigStr[:previewLen], len(sigStr))
			// Use it as-is - the SDK should have signed it correctly
		} else {
			// Not a string - might be bytes or something else
			fmt.Fprintf(os.Stderr, "[DEBUG] Sig is not a string, converting...\n")
			// Extract signature from the transaction object
			var sig []byte
			switch tx := txInfo.(type) {
			case *lightertxtypes.L2UpdateMarginTxInfo:
				sig = tx.Sig
			case *lightertxtypes.L2UpdateLeverageTxInfo:
				sig = tx.Sig
			default:
				return "", fmt.Errorf("unsupported transaction type")
			}
			// Convert to base64
			sigBase64 := base64.StdEncoding.EncodeToString(sig)
			txPayload["Sig"] = sigBase64
			fmt.Fprintf(os.Stderr, "[DEBUG] Converted signature to base64: %s\n", sigBase64)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG] No Sig field in GetTxInfo() output, adding it...\n")
		// Extract signature and add it
		var sig []byte
		switch tx := txInfo.(type) {
		case *lightertxtypes.L2UpdateMarginTxInfo:
			sig = tx.Sig
		case *lightertxtypes.L2UpdateLeverageTxInfo:
			sig = tx.Sig
		default:
			return "", fmt.Errorf("unsupported transaction type")
		}
		sigBase64 := base64.StdEncoding.EncodeToString(sig)
		txPayload["Sig"] = sigBase64
		fmt.Fprintf(os.Stderr, "[DEBUG] Added signature as base64: %s\n", sigBase64)
	}

	// Get tx_type from the transaction info
	var txType uint8
	switch tx := txInfo.(type) {
	case *lightertxtypes.L2UpdateMarginTxInfo:
		txType = tx.GetTxType() // Returns 29 (TxTypeL2UpdateMargin)
	case *lightertxtypes.L2UpdateLeverageTxInfo:
		txType = tx.GetTxType() // Returns 20 (TxTypeL2UpdateLeverage)
	default:
		return "", fmt.Errorf("unsupported transaction type")
	}

	// Debug: log the nonce being used
	if nonceVal, ok := txPayload["Nonce"]; ok {
		fmt.Fprintf(os.Stderr, "[DEBUG] Nonce in tx_info: %v\n", nonceVal)
	}

	// Re-marshal tx_info as a JSON string (this is what the API expects!)
	txInfoBytes, err := json.Marshal(txPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tx_info: %w", err)
	}
	txInfoString := string(txInfoBytes)

	// Build the final payload matching the frontend format:
	// {
	//   "tx_type": "20",  // String!
	//   "tx_info": "{...}",  // JSON string!
	//   "price_protection": "false"
	// }
	finalPayload := map[string]interface{}{
		"tx_type":          fmt.Sprintf("%d", txType), // String, not number!
		"tx_info":          txInfoString,              // JSON string, not object!
		"price_protection": "false",
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Final transaction payload: %s\n", func() string {
		body, _ := json.Marshal(finalPayload)
		return string(body)
	}())

	// Send using SendTxRaw
	txHash, err := c.apiClient.SendTxRaw(ctx, finalPayload, authToken)
	if err != nil {
		return "", fmt.Errorf("failed to send transaction: %w", err)
	}

	return txHash, nil
}

// toSnakeCase converts CamelCase to snake_case (helper function)
func toSnakeCase(s string) string {
	var result []rune
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result = append(result, '_')
		}
		if r >= 'A' && r <= 'Z' {
			result = append(result, r+'a'-'A')
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}
