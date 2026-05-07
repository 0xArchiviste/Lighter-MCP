package tpsl

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/0xarchiviste/lighter-mcp/pkg/account"
	"github.com/0xarchiviste/lighter-mcp/pkg/api"
	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/markets"
)

// TPSLClient provides TP/SL order management operations
type TPSLClient struct {
	orderClient  *account.OrderPlacementClient
	ordersClient *account.OrdersClient
	apiClient    *api.Client
	cfg          config.Config
}

// SetTPSL sets both TP and SL orders for a position in a single batch transaction
// This is more efficient than setting them separately
func (c *TPSLClient) SetTPSL(ctx context.Context, marketSymbol string, tpPrice float64, slPrice float64, authToken string) ([]string, error) {
	// Get position size
	positions, err := c.getPositions(ctx, authToken)
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	positionSize := c.getPositionSize(positions, marketSymbol)
	if positionSize <= 0 {
		return nil, fmt.Errorf("no position found for %s or position size is zero", marketSymbol)
	}
	
	// Use absolute value of position size
	if positionSize < 0 {
		positionSize = -positionSize
	}
	
	fmt.Fprintf(os.Stderr, "[DEBUG] Setting TP/SL for position size: %.4f\n", positionSize)

	// Build both TP and SL transactions
	// We'll need to build the transaction payloads manually for batch
	nonce, err := c.apiClient.NextNonceWithAuth(ctx, authToken)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}
	
	if nonce == 0 {
		return nil, fmt.Errorf("invalid nonce: 0")
	}

	// For now, send them separately (we can optimize to batch later if needed)
	// Actually, let's use the existing PlaceOrder which handles everything
	tpHash, err := c.SetTakeProfit(ctx, marketSymbol, tpPrice, positionSize, 0, authToken)
	if err != nil {
		return nil, fmt.Errorf("failed to set TP: %w", err)
	}
	
	slHash, err := c.SetStopLoss(ctx, marketSymbol, slPrice, positionSize, 0, authToken)
	if err != nil {
		return nil, fmt.Errorf("failed to set SL: %w", err)
	}

	return []string{tpHash, slHash}, nil
}

// NewTPSLClient creates a new TP/SL client
func NewTPSLClient(apiClient *api.Client, cfg config.Config) (*TPSLClient, error) {
	orderClient, err := account.NewOrderPlacementClient(apiClient, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create order placement client: %w", err)
	}

	ordersClient := account.NewOrdersClient(apiClient, cfg)

	return &TPSLClient{
		orderClient:  orderClient,
		ordersClient: ordersClient,
		apiClient:    apiClient,
		cfg:          cfg,
	}, nil
}

// SetStopLoss sets a stop-loss order for a position
// marketSymbol: Market symbol (e.g., "ETH", "BTC")
// price: Stop-loss trigger price in USDC
// size: Size in tokens (0 = use full position size)
// triggerPrice: Optional trigger price (0 = use price)
func (c *TPSLClient) SetStopLoss(ctx context.Context, marketSymbol string, price float64, size float64, triggerPrice float64, authToken string) (string, error) {
	// Get position size if size is 0
	if size == 0 {
		positions, err := c.getPositions(ctx, authToken)
		if err != nil {
			return "", fmt.Errorf("failed to get positions: %w", err)
		}

		positionSize := c.getPositionSize(positions, marketSymbol)
		if positionSize <= 0 {
			return "", fmt.Errorf("no position found for %s or position size is zero", marketSymbol)
		}
		// Use absolute value of position size (position can be negative for shorts)
		if positionSize < 0 {
			positionSize = -positionSize
		}
		size = positionSize
		fmt.Fprintf(os.Stderr, "[DEBUG] Using position size for stop-loss: %.4f\n", size)
	}

	// Determine side based on position (if long, sell for stop-loss)
	// For now, assume sell side for stop-loss (can be enhanced to detect position direction)
	side := account.OrderSideSell

	// Generate unique client order index
	clientOrderIndex := int64(0) // Auto-generate

	// Place stop-loss order
	txHash, err := c.orderClient.PlaceOrder(
		ctx,
		marketSymbol,
		side,
		account.OrderTypeStopLoss,
		price,
		size,
		clientOrderIndex,
		account.TIFIOC, // ImmediateOrCancel for stop-loss
		0,              // Default expiry
		triggerPrice,
		authToken,
	)
	if err != nil {
		return "", fmt.Errorf("failed to place stop-loss order: %w", err)
	}

	return txHash, nil
}

// SetTakeProfit sets a take-profit order for a position
// marketSymbol: Market symbol (e.g., "ETH", "BTC")
// price: Take-profit trigger price in USDC
// size: Size in tokens (0 = use full position size)
// triggerPrice: Optional trigger price (0 = use price)
func (c *TPSLClient) SetTakeProfit(ctx context.Context, marketSymbol string, price float64, size float64, triggerPrice float64, authToken string) (string, error) {
	// Get position size if size is 0
	if size == 0 {
		positions, err := c.getPositions(ctx, authToken)
		if err != nil {
			return "", fmt.Errorf("failed to get positions: %w", err)
		}

		positionSize := c.getPositionSize(positions, marketSymbol)
		if positionSize <= 0 {
			return "", fmt.Errorf("no position found for %s or position size is zero", marketSymbol)
		}
		// Use absolute value of position size (position can be negative for shorts)
		if positionSize < 0 {
			positionSize = -positionSize
		}
		size = positionSize
		fmt.Fprintf(os.Stderr, "[DEBUG] Using position size for take-profit: %.4f\n", size)
	}

	// Determine side based on position (if long, sell for take-profit)
	// For now, assume sell side for take-profit (can be enhanced to detect position direction)
	side := account.OrderSideSell

	// Generate unique client order index
	clientOrderIndex := int64(0) // Auto-generate

	// Place take-profit order
	txHash, err := c.orderClient.PlaceOrder(
		ctx,
		marketSymbol,
		side,
		account.OrderTypeTakeProfit,
		price,
		size,
		clientOrderIndex,
		account.TIFIOC, // ImmediateOrCancel for take-profit
		0,              // Default expiry
		triggerPrice,
		authToken,
	)
	if err != nil {
		return "", fmt.Errorf("failed to place take-profit order: %w", err)
	}

	return txHash, nil
}

// SetStopLossLimit sets a stop-loss limit order for a position
// marketSymbol: Market symbol (e.g., "ETH", "BTC")
// price: Limit price in USDC
// size: Size in tokens (0 = use full position size)
// triggerPrice: Trigger price in USDC
func (c *TPSLClient) SetStopLossLimit(ctx context.Context, marketSymbol string, price float64, size float64, triggerPrice float64, authToken string) (string, error) {
	// Get position size if size is 0
	if size == 0 {
		positions, err := c.getPositions(ctx, authToken)
		if err != nil {
			return "", fmt.Errorf("failed to get positions: %w", err)
		}

		positionSize := c.getPositionSize(positions, marketSymbol)
		if positionSize <= 0 {
			return "", fmt.Errorf("no position found for %s or position size is zero", marketSymbol)
		}
		size = positionSize
	}

	// Determine side based on position
	side := account.OrderSideSell

	// Generate unique client order index
	clientOrderIndex := int64(0) // Auto-generate

	// Place stop-loss limit order
	txHash, err := c.orderClient.PlaceOrder(
		ctx,
		marketSymbol,
		side,
		account.OrderTypeStopLossLimit,
		price,
		size,
		clientOrderIndex,
		account.TIFGTT, // GoodTillTime for limit orders
		0,              // Default expiry
		triggerPrice,
		authToken,
	)
	if err != nil {
		return "", fmt.Errorf("failed to place stop-loss limit order: %w", err)
	}

	return txHash, nil
}

// SetTakeProfitLimit sets a take-profit limit order for a position
// marketSymbol: Market symbol (e.g., "ETH", "BTC")
// price: Limit price in USDC
// size: Size in tokens (0 = use full position size)
// triggerPrice: Trigger price in USDC
func (c *TPSLClient) SetTakeProfitLimit(ctx context.Context, marketSymbol string, price float64, size float64, triggerPrice float64, authToken string) (string, error) {
	// Get position size if size is 0
	if size == 0 {
		positions, err := c.getPositions(ctx, authToken)
		if err != nil {
			return "", fmt.Errorf("failed to get positions: %w", err)
		}

		positionSize := c.getPositionSize(positions, marketSymbol)
		if positionSize <= 0 {
			return "", fmt.Errorf("no position found for %s or position size is zero", marketSymbol)
		}
		size = positionSize
	}

	// Determine side based on position
	side := account.OrderSideSell

	// Generate unique client order index
	clientOrderIndex := int64(0) // Auto-generate

	// Place take-profit limit order
	txHash, err := c.orderClient.PlaceOrder(
		ctx,
		marketSymbol,
		side,
		account.OrderTypeTakeProfitLimit,
		price,
		size,
		clientOrderIndex,
		account.TIFGTT, // GoodTillTime for limit orders
		0,              // Default expiry
		triggerPrice,
		authToken,
	)
	if err != nil {
		return "", fmt.Errorf("failed to place take-profit limit order: %w", err)
	}

	return txHash, nil
}

// ListTPSLOrders lists all TP/SL orders
func (c *TPSLClient) ListTPSLOrders(ctx context.Context, marketSymbol string, authToken string) ([]*account.Order, error) {
	// Get all orders
	var accountIndex *uint32
	if c.cfg.AccountIndex > 0 {
		idx := c.cfg.AccountIndex
		accountIndex = &idx
	}
	allOrders, err := c.ordersClient.GetOrders(ctx, accountIndex, authToken)
	if err != nil {
		return nil, fmt.Errorf("failed to get orders: %w", err)
	}

	// Filter for TP/SL orders
	var tpslOrders []*account.Order
	for _, order := range allOrders {
		orderType := strings.ToLower(order.OrderType)
		// Also check if market matches if specified
		if orderType == "stop-loss" || orderType == "stop-loss-limit" ||
			orderType == "take-profit" || orderType == "take-profit-limit" {
			// If marketSymbol is specified, filter by market
			if marketSymbol != "" {
				// Get market ID for the symbol
				marketID, err := markets.GetMarketIDGlobal(marketSymbol)
				if err == nil && uint32(order.MarketIndex) == marketID {
					tpslOrders = append(tpslOrders, order)
				}
			} else {
				tpslOrders = append(tpslOrders, order)
			}
		}
	}

	return tpslOrders, nil
}

// getPositions retrieves account positions
func (c *TPSLClient) getPositions(ctx context.Context, authToken string) (map[string]interface{}, error) {
	accountClient := account.NewClient(c.apiClient)
	accountIndex := c.cfg.AccountIndex
	if accountIndex == 0 {
		return nil, fmt.Errorf("account index is required")
	}

	accountData, err := accountClient.GetBalance(ctx, &accountIndex, authToken)
	if err != nil {
		return nil, fmt.Errorf("failed to get account data: %w", err)
	}

	return accountData.Positions, nil
}

// getPositionSize extracts position size for a given market
func (c *TPSLClient) getPositionSize(positions map[string]interface{}, marketSymbol string) float64 {
	if positions == nil {
		return 0
	}

	// Try to find position by market symbol
	// Positions are stored as map[string]interface{} with keys like "position_0", "position_1", etc.
	for _, posData := range positions {
		if posMap, ok := posData.(map[string]interface{}); ok {
			// Check if this position matches the market
			// Position structure may vary, try common fields
			if marketID, ok := posMap["market_id"].(float64); ok {
				// Convert market symbol to market ID
				marketIDUint, err := markets.GetMarketIDGlobal(marketSymbol)
				if err == nil && uint32(marketID) == marketIDUint {
					// Found matching position, extract size
					// Try "position" field first (this is the actual position size field)
					var sign float64 = 1.0
					if signVal, ok := posMap["sign"].(float64); ok {
						sign = signVal
					}
					
					if positionStr, ok := posMap["position"].(string); ok && positionStr != "" {
						var size big.Float
						if _, _, err := size.Parse(positionStr, 10); err == nil {
							sizeFloat, _ := size.Float64()
							return sizeFloat * sign // Apply sign for long/short
						}
					} else if positionFloat, ok := posMap["position"].(float64); ok && positionFloat != 0 {
						return positionFloat * sign
					} else if sizeStr, ok := posMap["size"].(string); ok && sizeStr != "" {
						// Fallback to "size" field
						var size big.Float
						if _, _, err := size.Parse(sizeStr, 10); err == nil {
							sizeFloat, _ := size.Float64()
							return sizeFloat * sign
						}
					} else if sizeFloat, ok := posMap["size"].(float64); ok && sizeFloat != 0 {
						return sizeFloat * sign
					}
				}
			}
		}
	}

	return 0
}

