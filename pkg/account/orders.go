package account

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	lighterclient "github.com/elliottech/lighter-go/client"
	"github.com/0xarchiviste/lighter-mcp/pkg/api"
	"github.com/0xarchiviste/lighter-mcp/pkg/config"
)

// Order represents a single order
type Order struct {
	ClientOrderIndex uint64    `json:"client_order_index"`
	OrderIndex       uint64    `json:"order_index"`
	MarketIndex      uint8     `json:"market_index"`
	Side             string    `json:"side"`              // "buy" or "sell"
	OrderType        string    `json:"order_type"`       // "LIMIT", "MARKET", etc.
	Price            *big.Int  `json:"price"`            // Price in micro-USDC (1 USDC = 1,000,000)
	Size             *big.Int  `json:"size"`             // Size in micro-tokens
	FilledSize       *big.Int  `json:"filled_size"`      // Filled size in micro-tokens
	Status           string    `json:"status"`           // "open", "filled", "cancelled", etc.
	TimeInForce      string    `json:"time_in_force"`     // "IMMEDIATE_OR_CANCEL", "GOOD_TILL_TIME", etc.
	ExpiredAt        int64     `json:"expired_at"`       // Expiration timestamp (milliseconds)
	CreatedAt        int64     `json:"created_at"`       // Creation timestamp (milliseconds)
	Raw              json.RawMessage `json:"-"`           // Store raw order data
}

// OrdersClient provides order-related operations
type OrdersClient struct {
	apiClient *api.Client
	cfg       config.Config
	txClient  *lighterclient.TxClient // SDK TxClient for GetAuthToken
}

// NewOrdersClient creates a new orders client
// cfg parameter is required to create TxClient for SDK's GetAuthToken
func NewOrdersClient(apiClient *api.Client, cfg config.Config) *OrdersClient {
	// Create TxClient for using SDK's GetAuthToken method
	// This ensures we use the official SDK method for auth tokens
	var txClient *lighterclient.TxClient
	if cfg.APIKeyPrivateKey != "" && cfg.AccountIndex > 0 {
		httpClient := apiClient.GetSDKClient()
		if httpClient != nil {
			// Create TxClient using SDK (same as margin client)
			var err error
			txClient, err = lighterclient.NewTxClient(
				httpClient,
				cfg.APIKeyPrivateKey,
				int64(cfg.AccountIndex),
				uint8(cfg.APIKeyIndex),
				cfg.ChainID,
			)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[DEBUG] Failed to create TxClient for orders: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "[DEBUG] Created TxClient for orders (account_index=%d, api_key_index=%d)\n", cfg.AccountIndex, cfg.APIKeyIndex)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] SDK HTTP client not available for orders TxClient\n")
		}
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG] Cannot create TxClient for orders: APIKeyPrivateKey=%v, AccountIndex=%d\n", cfg.APIKeyPrivateKey != "", cfg.AccountIndex)
	}

	return &OrdersClient{
		apiClient: apiClient,
		cfg:       cfg,
		txClient:  txClient,
	}
}

// GetAuthToken uses the SDK's TxClient.GetAuthToken method if available
// Falls back to using the provided authToken if TxClient is not available
func (c *OrdersClient) GetAuthToken(expirySeconds int64) (string, error) {
	if c.txClient != nil {
		// Use SDK's official GetAuthToken method
		deadline := time.Now().Add(time.Duration(expirySeconds) * time.Second)
		return c.txClient.GetAuthToken(deadline)
	}
	// Fallback: return empty string, caller should provide authToken
	return "", fmt.Errorf("TxClient not available, please provide authToken")
}

// GetOrders retrieves all orders for an account
func (c *OrdersClient) GetOrders(ctx context.Context, accountIndex *uint32, authToken string) ([]*Order, error) {
	// If no authToken provided and we have TxClient, generate one using SDK's GetAuthToken
	if authToken == "" && c.txClient != nil {
		var err error
		authToken, err = c.GetAuthToken(3600) // 1 hour expiry
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARNING] Failed to generate auth token using TxClient.GetAuthToken: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] Generated auth token using SDK's TxClient.GetAuthToken\n")
		}
	}
	// Try to fetch orders via account data endpoint
	// The orders are typically included in the account data response
	rawData, err := c.apiClient.AccountData(ctx, "", accountIndex, authToken)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch account data: %w", err)
	}

	// Parse the response
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

	// Extract orders
	var orders []*Order
	
	// Try to find orders in different possible locations
	if ordersArray, ok := accountMap["orders"].([]interface{}); ok {
		fmt.Fprintf(os.Stderr, "[DEBUG] Found %d orders in account data (orders field)\n", len(ordersArray))
		orders = parseOrdersArray(ordersArray)
	} else if ordersArray, ok := accountMap["open_orders"].([]interface{}); ok {
		fmt.Fprintf(os.Stderr, "[DEBUG] Found %d orders in account data (open_orders field)\n", len(ordersArray))
		orders = parseOrdersArray(ordersArray)
	} else if ordersArray, ok := accountMap["active_orders"].([]interface{}); ok {
		fmt.Fprintf(os.Stderr, "[DEBUG] Found %d orders in account data (active_orders field)\n", len(ordersArray))
		orders = parseOrdersArray(ordersArray)
	} else {
		fmt.Fprintf(os.Stderr, "[DEBUG] No orders array found in account data. Checking positions for nested orders...\n")
		
		// Check if orders are nested within positions
		if positions, ok := accountMap["positions"].([]interface{}); ok {
			for i, pos := range positions {
				if posMap, ok := pos.(map[string]interface{}); ok {
					// Check for orders nested in position
					if posOrders, ok := posMap["orders"].([]interface{}); ok {
						fmt.Fprintf(os.Stderr, "[DEBUG] Found %d orders in position %d\n", len(posOrders), i)
						orders = append(orders, parseOrdersArray(posOrders)...)
					}
					// Check for open_order_count to see if there are orders
					if openCount, ok := posMap["open_order_count"].(float64); ok && openCount > 0 {
						fmt.Fprintf(os.Stderr, "[DEBUG] Position %d has %d open orders (but orders array not found)\n", i, int(openCount))
					}
				}
			}
		}
		
		// Debug: log all keys in accountMap to see what's available
		if len(orders) == 0 {
			fmt.Fprintf(os.Stderr, "[DEBUG] No orders found. Account data fields:\n")
			for key := range accountMap {
				fmt.Fprintf(os.Stderr, "[DEBUG] Account data field: %s\n", key)
			}
		}
	}

	// If no orders found in account data, try dedicated orders endpoint
	// NOTE: The /api/v1/accountOrders endpoint returns 403 Forbidden, suggesting:
	// 1. The endpoint exists but requires different authentication/permissions
	// 2. The endpoint might not be publicly available
	// 3. Orders might only be accessible via WebSocket subscriptions
	// We'll still try it but gracefully handle the 403 error
	if len(orders) == 0 {
		fmt.Fprintf(os.Stderr, "[DEBUG] Attempting to fetch orders from dedicated endpoint...\n")
		orders, err = c.fetchOrdersFromEndpoint(ctx, accountIndex, authToken)
		if err != nil {
			// Check if it's a 403 error (permission denied)
			if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "Forbidden") {
				fmt.Fprintf(os.Stderr, "[WARNING] Orders endpoint returned 403 Forbidden. This endpoint may require special permissions or may not be publicly available.\n")
				fmt.Fprintf(os.Stderr, "[INFO] Order counts are available in account data: pending_order_count, total_order_count, open_order_count (per position)\n")
			} else {
				fmt.Fprintf(os.Stderr, "[DEBUG] Could not fetch orders from dedicated endpoint: %v\n", err)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] Successfully fetched %d orders from dedicated endpoint\n", len(orders))
		}
	}

	return orders, nil
}

// fetchOrdersFromEndpoint tries to fetch orders from a dedicated orders endpoint
// Uses the correct Python SDK endpoints: accountActiveOrders and accountInactiveOrders
func (c *OrdersClient) fetchOrdersFromEndpoint(ctx context.Context, accountIndex *uint32, authToken string) ([]*Order, error) {
	if accountIndex == nil {
		return nil, fmt.Errorf("account_index is required")
	}
	
	// Use SDK's GetAuthToken if available (generates fresh token with correct format)
	if c.txClient != nil {
		deadline := time.Now().Add(1 * time.Hour)
		sdkAuthToken, err := c.txClient.GetAuthToken(deadline)
		if err == nil {
			fmt.Fprintf(os.Stderr, "[DEBUG] Using SDK's GetAuthToken for orders endpoint\n")
			authToken = sdkAuthToken
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] Failed to generate SDK auth token: %v, using provided token\n", err)
		}
	}
	
	// Try active orders first - try market_id=0 (ETH) first, then try 255 (all markets)
	// Note: We fetch all markets (255) to get all orders, then filter by market_id if needed
	fmt.Fprintf(os.Stderr, "[DEBUG] Fetching active orders from /api/v1/accountActiveOrders (market_id=0 for ETH)\n")
	activeData, err := c.apiClient.AccountActiveOrders(ctx, *accountIndex, 0, authToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Active orders failed for market_id=0: %v, trying market_id=255...\n", err)
		// Try market_id=255 (all markets) as fallback
		activeData, err = c.apiClient.AccountActiveOrders(ctx, *accountIndex, 255, authToken)
	}
	if err == nil {
		return c.parseOrdersResponse(activeData)
	}
	fmt.Fprintf(os.Stderr, "[DEBUG] Active orders failed: %v, trying inactive orders...\n", err)
	
	// Try inactive orders with limit=100
	fmt.Fprintf(os.Stderr, "[DEBUG] Fetching inactive orders from /api/v1/accountInactiveOrders\n")
	inactiveData, err := c.apiClient.AccountInactiveOrders(ctx, *accountIndex, 100, authToken, nil)
	if err == nil {
		return c.parseOrdersResponse(inactiveData)
	}
	
	return nil, fmt.Errorf("failed to fetch orders from both endpoints: active: %v, inactive: %v", err, err)
}

// parseOrdersResponse parses the orders response from the API
func (c *OrdersClient) parseOrdersResponse(rawData json.RawMessage) ([]*Order, error) {
	var ordersData interface{}
	if err := json.Unmarshal(rawData, &ordersData); err != nil {
		return nil, fmt.Errorf("failed to parse orders data: %w", err)
	}

	// Handle different response formats
	// Python SDK returns Orders object which may have orders array
	if ordersArray, ok := ordersData.([]interface{}); ok {
		return parseOrdersArray(ordersArray), nil
	} else if ordersMap, ok := ordersData.(map[string]interface{}); ok {
		// Check for "orders" field (Python SDK Orders model)
		if ordersArray, ok := ordersMap["orders"].([]interface{}); ok {
			fmt.Fprintf(os.Stderr, "[DEBUG] Found %d orders in 'orders' field\n", len(ordersArray))
			return parseOrdersArray(ordersArray), nil
		} else if ordersArray, ok := ordersMap["data"].([]interface{}); ok {
			fmt.Fprintf(os.Stderr, "[DEBUG] Found %d orders in 'data' field\n", len(ordersArray))
			return parseOrdersArray(ordersArray), nil
		} else if ordersArray, ok := ordersMap["active_orders"].([]interface{}); ok {
			fmt.Fprintf(os.Stderr, "[DEBUG] Found %d orders in 'active_orders' field\n", len(ordersArray))
			return parseOrdersArray(ordersArray), nil
		} else if ordersArray, ok := ordersMap["inactive_orders"].([]interface{}); ok {
			fmt.Fprintf(os.Stderr, "[DEBUG] Found %d orders in 'inactive_orders' field\n", len(ordersArray))
			return parseOrdersArray(ordersArray), nil
		}
		// Debug: log all keys to see what's available
		fmt.Fprintf(os.Stderr, "[DEBUG] Orders response keys: %v\n", getMapKeys(ordersMap))
	}

	return nil, fmt.Errorf("unexpected orders response format")
}

// getMapKeys returns all keys from a map
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// parseOrdersArray parses an array of order objects
func parseOrdersArray(ordersArray []interface{}) []*Order {
	var orders []*Order
	
	for _, orderData := range ordersArray {
		orderMap, ok := orderData.(map[string]interface{})
		if !ok {
			continue
		}

		order := &Order{}
		
		// Store raw order data
		if rawBytes, err := json.Marshal(orderData); err == nil {
			order.Raw = rawBytes
		}

		// Parse client_order_index
		if idx, ok := orderMap["client_order_index"].(float64); ok {
			order.ClientOrderIndex = uint64(idx)
		} else if idx, ok := orderMap["clientOrderIndex"].(float64); ok {
			order.ClientOrderIndex = uint64(idx)
		}

		// Parse order_index
		if idx, ok := orderMap["order_index"].(float64); ok {
			order.OrderIndex = uint64(idx)
		} else if idx, ok := orderMap["orderIndex"].(float64); ok {
			order.OrderIndex = uint64(idx)
		}

		// Parse market_index
		if idx, ok := orderMap["market_index"].(float64); ok {
			order.MarketIndex = uint8(idx)
		} else if idx, ok := orderMap["marketIndex"].(float64); ok {
			order.MarketIndex = uint8(idx)
		}

		// Parse side - API uses is_ask: false = buy, true = sell
		// Also check side field (may be empty string)
		if isAsk, ok := orderMap["is_ask"].(bool); ok {
			if isAsk {
				order.Side = "sell"
			} else {
				order.Side = "buy"
			}
		} else if side, ok := orderMap["side"].(string); ok && side != "" {
			order.Side = side
		} else if side, ok := orderMap["side"].(float64); ok {
			// Sometimes side is numeric: 0 = buy, 1 = sell
			if side == 0 {
				order.Side = "buy"
			} else {
				order.Side = "sell"
			}
		}

		// Parse order_type - API uses "type" field
		if orderType, ok := orderMap["type"].(string); ok {
			order.OrderType = orderType
		} else if orderType, ok := orderMap["order_type"].(string); ok {
			order.OrderType = orderType
		} else if orderType, ok := orderMap["orderType"].(string); ok {
			order.OrderType = orderType
		}

		// Parse price - API returns "price" as string in USDC format (e.g., "3001.00")
		// Also check base_price (int, seems to be in micro-USDC: 300100 = 3001.00)
		if priceStr, ok := orderMap["price"].(string); ok && priceStr != "" {
			// Price is already in USDC format, convert to micro-USDC (multiply by 1,000,000)
			priceFloat, _, err := big.ParseFloat(priceStr, 10, 256, big.ToNearestEven)
			if err == nil {
				// Convert to micro-USDC (multiply by 1,000,000)
				microUSDC := new(big.Float).Mul(priceFloat, big.NewFloat(1000000))
				priceInt, _ := microUSDC.Int(nil)
				order.Price = priceInt
			}
		} else if basePrice, ok := orderMap["base_price"].(float64); ok {
			// base_price appears to be in micro-USDC already
			order.Price = big.NewInt(int64(basePrice))
		} else if price, ok := orderMap["price"].(float64); ok {
			order.Price = big.NewInt(int64(price))
		}

		// Parse size - API uses "initial_base_amount" or "remaining_base_amount" (string, in tokens)
		// Also check base_size (int, seems to be in micro-tokens: 10000 = 1.0)
		if sizeStr, ok := orderMap["remaining_base_amount"].(string); ok && sizeStr != "" {
			// Size is in tokens, convert to micro-tokens (multiply by 1,000,000)
			sizeFloat, _, err := big.ParseFloat(sizeStr, 10, 256, big.ToNearestEven)
			if err == nil {
				microTokens := new(big.Float).Mul(sizeFloat, big.NewFloat(1000000))
				sizeInt, _ := microTokens.Int(nil)
				order.Size = sizeInt
			}
		} else if sizeStr, ok := orderMap["initial_base_amount"].(string); ok && sizeStr != "" {
			// Fallback to initial_base_amount
			sizeFloat, _, err := big.ParseFloat(sizeStr, 10, 256, big.ToNearestEven)
			if err == nil {
				microTokens := new(big.Float).Mul(sizeFloat, big.NewFloat(1000000))
				sizeInt, _ := microTokens.Int(nil)
				order.Size = sizeInt
			}
		} else if baseSize, ok := orderMap["base_size"].(float64); ok {
			// base_size appears to be in micro-tokens already
			order.Size = big.NewInt(int64(baseSize))
		} else if sizeStr, ok := orderMap["size"].(string); ok {
			sizeFloat, _, err := big.ParseFloat(sizeStr, 10, 256, big.ToNearestEven)
			if err == nil {
				microTokens := new(big.Float).Mul(sizeFloat, big.NewFloat(1000000))
				sizeInt, _ := microTokens.Int(nil)
				order.Size = sizeInt
			}
		} else if size, ok := orderMap["size"].(float64); ok {
			order.Size = big.NewInt(int64(size))
		}

		// Parse filled_size - API uses "filled_base_amount" (string, in tokens)
		if filledStr, ok := orderMap["filled_base_amount"].(string); ok && filledStr != "" {
			// Filled size is in tokens, convert to micro-tokens
			filledFloat, _, err := big.ParseFloat(filledStr, 10, 256, big.ToNearestEven)
			if err == nil {
				microTokens := new(big.Float).Mul(filledFloat, big.NewFloat(1000000))
				filledInt, _ := microTokens.Int(nil)
				order.FilledSize = filledInt
			}
		} else if filledStr, ok := orderMap["filled_size"].(string); ok {
			filledFloat, _, err := big.ParseFloat(filledStr, 10, 256, big.ToNearestEven)
			if err == nil {
				microTokens := new(big.Float).Mul(filledFloat, big.NewFloat(1000000))
				filledInt, _ := microTokens.Int(nil)
				order.FilledSize = filledInt
			}
		} else if filledStr, ok := orderMap["filledSize"].(string); ok {
			filledFloat, _, err := big.ParseFloat(filledStr, 10, 256, big.ToNearestEven)
			if err == nil {
				microTokens := new(big.Float).Mul(filledFloat, big.NewFloat(1000000))
				filledInt, _ := microTokens.Int(nil)
				order.FilledSize = filledInt
			}
		} else if filled, ok := orderMap["filled_size"].(float64); ok {
			order.FilledSize = big.NewInt(int64(filled))
		} else if filled, ok := orderMap["filledSize"].(float64); ok {
			order.FilledSize = big.NewInt(int64(filled))
		}

		// Parse status
		if status, ok := orderMap["status"].(string); ok {
			order.Status = status
		} else if status, ok := orderMap["status"].(float64); ok {
			// Sometimes status is numeric
			order.Status = fmt.Sprintf("%.0f", status)
		}

		// Parse time_in_force
		if tif, ok := orderMap["time_in_force"].(string); ok {
			order.TimeInForce = tif
		} else if tif, ok := orderMap["timeInForce"].(string); ok {
			order.TimeInForce = tif
		}

		// Parse expired_at (milliseconds)
		if expired, ok := orderMap["expired_at"].(float64); ok {
			order.ExpiredAt = int64(expired)
		} else if expired, ok := orderMap["expiredAt"].(float64); ok {
			order.ExpiredAt = int64(expired)
		} else if expired, ok := orderMap["expired_at"].(int64); ok {
			order.ExpiredAt = expired
		}

		// Parse created_at (milliseconds)
		if created, ok := orderMap["created_at"].(float64); ok {
			order.CreatedAt = int64(created)
		} else if created, ok := orderMap["createdAt"].(float64); ok {
			order.CreatedAt = int64(created)
		} else if created, ok := orderMap["created_at"].(int64); ok {
			order.CreatedAt = created
		}

		orders = append(orders, order)
	}

	return orders
}

// parseMicroUSDC parses a string representing micro-USDC (1 USDC = 1,000,000 micro-USDC)
func parseMicroUSDC(s string) (*big.Int, error) {
	// Try parsing as decimal string first
	bi := new(big.Int)
	bi, ok := bi.SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("failed to parse micro-USDC: %s", s)
	}
	return bi, nil
}

// parseMicroTokens parses a string representing micro-tokens
func parseMicroTokens(s string) (*big.Int, error) {
	bi := new(big.Int)
	bi, ok := bi.SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("failed to parse micro-tokens: %s", s)
	}
	return bi, nil
}

// FormatPrice formats price from micro-USDC to USDC string
func (o *Order) FormatPrice() string {
	if o.Price == nil {
		return "0"
	}
	// Convert micro-USDC to USDC (divide by 1,000,000)
	usdc := new(big.Float).Quo(new(big.Float).SetInt(o.Price), big.NewFloat(1_000_000))
	return usdc.Text('f', 6)
}

// FormatSize formats size from micro-tokens to tokens string
func (o *Order) FormatSize() string {
	if o.Size == nil {
		return "0"
	}
	// Convert micro-tokens to tokens (divide by 1,000,000)
	tokens := new(big.Float).Quo(new(big.Float).SetInt(o.Size), big.NewFloat(1_000_000))
	return tokens.Text('f', 6)
}

// FormatFilledSize formats filled size from micro-tokens to tokens string
func (o *Order) FormatFilledSize() string {
	if o.FilledSize == nil {
		return "0"
	}
	// Convert micro-tokens to tokens (divide by 1,000,000)
	tokens := new(big.Float).Quo(new(big.Float).SetInt(o.FilledSize), big.NewFloat(1_000_000))
	return tokens.Text('f', 6)
}

// FormatExpiredAt formats expiration timestamp
func (o *Order) FormatExpiredAt() string {
	if o.ExpiredAt == 0 {
		return "Never"
	}
	t := time.Unix(o.ExpiredAt/1000, (o.ExpiredAt%1000)*1_000_000)
	return t.Format(time.RFC3339)
}

// FormatCreatedAt formats creation timestamp
func (o *Order) FormatCreatedAt() string {
	if o.CreatedAt == 0 {
		return "Unknown"
	}
	t := time.Unix(o.CreatedAt/1000, (o.CreatedAt%1000)*1_000_000)
	return t.Format(time.RFC3339)
}

// IsOpen returns true if the order is still open
func (o *Order) IsOpen() bool {
	return o.Status == "open" || o.Status == "pending" || o.Status == "active"
}

// IsFilled returns true if the order is fully filled
func (o *Order) IsFilled() bool {
	return o.Status == "filled" || o.Status == "completed"
}

// IsCancelled returns true if the order is cancelled
func (o *Order) IsCancelled() bool {
	return o.Status == "cancelled" || o.Status == "canceled"
}

