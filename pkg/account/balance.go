package account

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/0xarchiviste/lighter-mcp/pkg/api"
)

// Balance represents account balance information
type Balance struct {
	TotalBalance    *big.Int            `json:"total_balance"`
	AvailableBalance *big.Int            `json:"available_balance"`
	LockedBalance   *big.Int            `json:"locked_balance"`
	Tokens          map[string]*big.Int `json:"tokens"`
}

// AccountData represents full account information
type AccountData struct {
	AccountIndex uint32                 `json:"account_index"`
	L1Address    string                 `json:"l1_address"`
	Balance      *Balance               `json:"balance"`
	Positions    map[string]interface{} `json:"positions"`
	Orders       []interface{}          `json:"orders"`
	Raw          json.RawMessage         `json:"-"` // Store raw response
}

// Client provides account-related operations
type Client struct {
	apiClient *api.Client
}

// NewClient creates a new account client
func NewClient(apiClient *api.Client) *Client {
	return &Client{
		apiClient: apiClient,
	}
}

// GetBalance retrieves account balance information
func (c *Client) GetBalance(ctx context.Context, accountIndex *uint32, authToken string) (*AccountData, error) {
	// Fetch account data
	rawData, err := c.apiClient.AccountData(ctx, "", accountIndex, authToken)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch account data: %w", err)
	}

	// Parse account data
	var accountData AccountData
	accountData.Raw = rawData

	// Try to parse the response
	var responseMap map[string]interface{}
	if err := json.Unmarshal(rawData, &responseMap); err != nil {
		return nil, fmt.Errorf("failed to parse account data: %w", err)
	}

	// Handle different response formats
	var accountMap map[string]interface{}
	
	// Check if response has "accounts" array (API v1 format)
	if accounts, ok := responseMap["accounts"].([]interface{}); ok && len(accounts) > 0 {
		if acc, ok := accounts[0].(map[string]interface{}); ok {
			accountMap = acc
		}
	} else {
		// Direct account object format
		accountMap = responseMap
	}

	if accountMap == nil {
		return nil, fmt.Errorf("could not find account data in response")
	}

	// Extract account index
	if idx, ok := accountMap["index"].(float64); ok {
		accountData.AccountIndex = uint32(idx)
	} else if idx, ok := accountMap["account_index"].(float64); ok {
		accountData.AccountIndex = uint32(idx)
	}

	// Extract L1 address
	if addr, ok := accountMap["l1_address"].(string); ok {
		accountData.L1Address = addr
	}

	// Extract balance information
	accountData.Balance = &Balance{
		Tokens: make(map[string]*big.Int),
	}

	// Parse available balance (main balance field)
	// Note: API returns decimal strings, need to convert to wei (18 decimals)
	if available, ok := accountMap["available_balance"].(string); ok {
		accountData.Balance.AvailableBalance, _ = parseDecimalToWei(available)
		// If no total_balance, use available_balance as total
		if accountData.Balance.TotalBalance == nil {
			accountData.Balance.TotalBalance = accountData.Balance.AvailableBalance
		}
	}

	// Parse collateral (total asset value)
	if collateral, ok := accountMap["collateral"].(string); ok {
		accountData.Balance.TotalBalance, _ = parseDecimalToWei(collateral)
	}

	// Parse total asset value
	if totalAsset, ok := accountMap["total_asset_value"].(string); ok {
		accountData.Balance.TotalBalance, _ = parseDecimalToWei(totalAsset)
	}

	// Parse cross asset value
	if crossAsset, ok := accountMap["cross_asset_value"].(string); ok {
		// Could use this for available balance if available_balance is not present
		if accountData.Balance.AvailableBalance == nil {
			accountData.Balance.AvailableBalance, _ = parseDecimalToWei(crossAsset)
		}
	}

	// Calculate locked balance (total - available)
	if accountData.Balance.TotalBalance != nil && accountData.Balance.AvailableBalance != nil {
		locked := new(big.Int).Sub(accountData.Balance.TotalBalance, accountData.Balance.AvailableBalance)
		if locked.Sign() > 0 {
			accountData.Balance.LockedBalance = locked
		}
	}

	// Extract positions if available
	if positions, ok := accountMap["positions"].([]interface{}); ok {
		accountData.Positions = make(map[string]interface{})
		for i, pos := range positions {
			if posMap, ok := pos.(map[string]interface{}); ok {
				accountData.Positions[fmt.Sprintf("position_%d", i)] = posMap
			}
		}
	}

	// Extract order counts
	if orderCount, ok := accountMap["total_order_count"].(float64); ok {
		// Store order count info
		if accountData.Positions == nil {
			accountData.Positions = make(map[string]interface{})
		}
		accountData.Positions["total_order_count"] = uint32(orderCount)
	}

	return &accountData, nil
}

// parseBigInt parses a string into a big.Int
func parseBigInt(s string) (*big.Int, error) {
	bi := new(big.Int)
	bi, ok := bi.SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("failed to parse big int: %s", s)
	}
	return bi, nil
}

// ParseDecimalToWei parses a decimal string and converts it to wei (18 decimals)
// Exported for use in other packages
func ParseDecimalToWei(s string) (*big.Int, error) {
	return parseDecimalToWei(s)
}

// parseDecimalToWei parses a decimal string and converts it to wei (18 decimals)
func parseDecimalToWei(s string) (*big.Int, error) {
	// Parse as float
	val, _, err := new(big.Float).Parse(s, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to parse decimal: %s, error: %w", s, err)
	}
	
	// Multiply by 10^18 to convert to wei
	multiplier := new(big.Float).SetInt64(1e18)
	val.Mul(val, multiplier)
	
	// Convert to big.Int
	result := new(big.Int)
	val.Int(result)
	return result, nil
}

// FormatBalance formats balance for display
func (b *Balance) Format() string {
	if b == nil {
		return "N/A"
	}

	var result string
	if b.TotalBalance != nil {
		result += fmt.Sprintf("Total: %s", formatWei(b.TotalBalance))
	}
	if b.AvailableBalance != nil {
		result += fmt.Sprintf(" | Available: %s", formatWei(b.AvailableBalance))
	}
	if b.LockedBalance != nil {
		result += fmt.Sprintf(" | Locked: %s", formatWei(b.LockedBalance))
	}

	return result
}

// formatWei formats wei amount to a readable format
// Note: Lighter balances are in USD (with 18 decimals for precision)
func formatWei(amount *big.Int) string {
	if amount == nil {
		return "0"
	}
	
	// Convert wei to USD (assuming 18 decimals for precision)
	usd := new(big.Float).Quo(new(big.Float).SetInt(amount), big.NewFloat(1e18))
	return usd.Text('f', 2) + " USD"
}

