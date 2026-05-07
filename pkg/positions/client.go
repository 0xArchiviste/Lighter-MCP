package positions

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/0xarchiviste/lighter-mcp/pkg/account"
	"github.com/0xarchiviste/lighter-mcp/pkg/api"
	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/markets"
)

// Position represents a trading position
type Position struct {
	MarketID      uint32   `json:"market_id"`
	MarketSymbol  string   `json:"market_symbol"`
	Size          *big.Float `json:"size"`           // Position size (positive = long, negative = short)
	EntryPrice    *big.Float `json:"entry_price"`    // Average entry price
	MarkPrice     *big.Float `json:"mark_price"`    // Current mark price
	LiquidationPrice *big.Float `json:"liquidation_price"` // Liquidation price
	UnrealizedPnl *big.Float `json:"unrealized_pnl"` // Unrealized P&L
	MarginUsed    *big.Float `json:"margin_used"`    // Margin used for this position
	Leverage      float64   `json:"leverage"`       // Leverage (e.g., 2.0 for 2x)
	Raw           json.RawMessage `json:"-"`         // Store raw position data
}

// PositionsClient provides position management operations
type PositionsClient struct {
	orderClient  *account.OrderPlacementClient
	apiClient    *api.Client
	cfg          config.Config
}

// NewPositionsClient creates a new positions client
func NewPositionsClient(apiClient *api.Client, cfg config.Config) (*PositionsClient, error) {
	orderClient, err := account.NewOrderPlacementClient(apiClient, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create order placement client: %w", err)
	}

	return &PositionsClient{
		orderClient: orderClient,
		apiClient:   apiClient,
		cfg:         cfg,
	}, nil
}

// ListPositions lists all open positions
func (c *PositionsClient) ListPositions(ctx context.Context, marketSymbol string, authToken string) ([]*Position, error) {
	accountClient := account.NewClient(c.apiClient)
	accountIndex := c.cfg.AccountIndex
	if accountIndex == 0 {
		return nil, fmt.Errorf("account index is required")
	}

	accountData, err := accountClient.GetBalance(ctx, &accountIndex, authToken)
	if err != nil {
		return nil, fmt.Errorf("failed to get account data: %w", err)
	}

	positions, err := parsePositions(accountData.Positions)
	if err != nil {
		return nil, fmt.Errorf("failed to parse positions: %w", err)
	}

	// Filter by market if specified
	if marketSymbol != "" {
		marketID, err := markets.GetMarketIDGlobal(marketSymbol)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve market ID: %w", err)
		}

		var filtered []*Position
		for _, pos := range positions {
			if pos.MarketID == marketID {
				filtered = append(filtered, pos)
			}
		}
		return filtered, nil
	}

	return positions, nil
}

// AddToPosition adds to an existing position by placing an order
// If position doesn't exist, opens a new position
func (c *PositionsClient) AddToPosition(ctx context.Context, marketSymbol string, side account.OrderSide, size float64, orderType account.OrderType, price float64, authToken string) (string, error) {
	// Place an order to add to the position
	// For long positions: place buy orders
	// For short positions: place sell orders
	clientOrderIndex := int64(0) // Auto-generate
	timeInForce := account.TIFGTT // Good Till Time
	orderExpiry := int64(0) // Default expiry
	triggerPrice := float64(0) // No trigger price

	txHash, err := c.orderClient.PlaceOrder(
		ctx,
		marketSymbol,
		side,
		orderType,
		price,
		size,
		clientOrderIndex,
		timeInForce,
		orderExpiry,
		triggerPrice,
		authToken,
	)
	if err != nil {
		return "", fmt.Errorf("failed to place order to add to position: %w", err)
	}

	return txHash, nil
}

// ModifyPosition modifies an existing position
// This can be used to:
// - Reduce position size (place opposite side order)
// - Close position entirely (place opposite side order with full size)
func (c *PositionsClient) ModifyPosition(ctx context.Context, marketSymbol string, newSize float64, orderType account.OrderType, price float64, authToken string) (string, error) {
	// Get current position
	positions, err := c.ListPositions(ctx, marketSymbol, authToken)
	if err != nil {
		return "", fmt.Errorf("failed to get positions: %w", err)
	}

	if len(positions) == 0 {
		return "", fmt.Errorf("no position found for %s", marketSymbol)
	}

	position := positions[0]
	currentSize, _ := position.Size.Float64()

	// Fix: Position size is in ten-thousandths, but newSize is in tokens
	// Convert currentSize from ten-thousandths to tokens for comparison
	currentSizeTokens := currentSize / 10_000

	// Calculate size difference (both in tokens now)
	sizeDiff := newSize - currentSizeTokens

	if sizeDiff == 0 {
		return "", fmt.Errorf("new size equals current size, no change needed")
	}

	// Determine side based on size difference
	var side account.OrderSide
	if sizeDiff > 0 {
		// Increasing position size
		if currentSizeTokens >= 0 {
			side = account.OrderSideBuy // Long position, buy more
		} else {
			side = account.OrderSideSell // Short position, sell more (more negative)
		}
	} else {
		// Decreasing position size
		if currentSizeTokens > 0 {
			side = account.OrderSideSell // Long position, sell to reduce
		} else {
			side = account.OrderSideBuy // Short position, buy to reduce (less negative)
		}
	}

	// Use absolute value for order size (already in tokens)
	orderSize := sizeDiff
	if orderSize < 0 {
		orderSize = -orderSize
	}

	clientOrderIndex := int64(0) // Auto-generate
	timeInForce := account.TIFGTT // Good Till Time
	orderExpiry := int64(0) // Default expiry
	triggerPrice := float64(0) // No trigger price

	txHash, err := c.orderClient.PlaceOrder(
		ctx,
		marketSymbol,
		side,
		orderType,
		price,
		orderSize,
		clientOrderIndex,
		timeInForce,
		orderExpiry,
		triggerPrice,
		authToken,
	)
	if err != nil {
		return "", fmt.Errorf("failed to place order to modify position: %w", err)
	}

	return txHash, nil
}

// ClosePosition closes a position entirely by placing an opposite market order immediately
// This always uses market orders with IOC time-in-force for immediate execution
func (c *PositionsClient) ClosePosition(ctx context.Context, marketSymbol string, authToken string) (string, error) {
	// Get current position
	positions, err := c.ListPositions(ctx, marketSymbol, authToken)
	if err != nil {
		return "", fmt.Errorf("failed to get positions: %w", err)
	}

	if len(positions) == 0 {
		return "", fmt.Errorf("no position found for %s", marketSymbol)
	}

	position := positions[0]
	currentSize, _ := position.Size.Float64()

	if currentSize == 0 {
		return "", fmt.Errorf("position is already closed")
	}

	// Debug logging - log raw position data to understand units
	fmt.Fprintf(os.Stderr, "[DEBUG ClosePosition] Market: %s\n", marketSymbol)
	fmt.Fprintf(os.Stderr, "[DEBUG ClosePosition] Parsed position size: %.6f\n", currentSize)
	if position.Raw != nil {
		var rawData map[string]interface{}
		if err := json.Unmarshal(position.Raw, &rawData); err == nil {
			fmt.Fprintf(os.Stderr, "[DEBUG ClosePosition] Raw position fields: %v\n", getMapKeys(rawData))
			if posVal, ok := rawData["position"]; ok {
				fmt.Fprintf(os.Stderr, "[DEBUG ClosePosition] Raw 'position' field: %v (type: %T)\n", posVal, posVal)
			}
		}
	}

	// Determine side (opposite of current position)
	var side account.OrderSide
	if currentSize > 0 {
		side = account.OrderSideSell // Long position, sell to close
	} else {
		side = account.OrderSideBuy // Short position, buy to close
	}

	// Use absolute value for order size
	orderSize := currentSize
	if orderSize < 0 {
		orderSize = -orderSize
	}

	// Fix: Position close is amplified by 10x, so divide by 10 to correct it
	orderSize = orderSize / 10

	fmt.Fprintf(os.Stderr, "[DEBUG ClosePosition] Position size (ten-thousandths): %.2f, Order size (tokens): %.6f\n", currentSize, orderSize)
	fmt.Fprintf(os.Stderr, "[DEBUG ClosePosition] Final order: side=%s, size=%.6f tokens\n", side, orderSize)

	// Always use market order with IOC for immediate closure
	txHash, err := c.orderClient.PlaceOrder(
		ctx,
		marketSymbol,
		side,
		account.OrderTypeMarket, // Always market order
		0,                        // Price not needed for market orders
		orderSize,
		0,        // Auto-generate client order index
		account.TIFIOC, // ImmediateOrCancel for immediate execution
		0,        // No expiry for IOC
		0,        // No trigger price
		authToken,
	)
	if err != nil {
		return "", fmt.Errorf("failed to place order to close position: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG ClosePosition] Order placed successfully: txHash=%s\n", txHash)
	return txHash, nil
}

// ClearPosition clears a position by:
// 1. Cancelling all TP/SL orders for the position
// 2. Closing the position entirely
func (c *PositionsClient) ClearPosition(ctx context.Context, marketSymbol string, orderType account.OrderType, price float64, authToken string) ([]string, error) {
	var txHashes []string

	// Step 1: Cancel all TP/SL orders for this market
	// Get all orders and filter TP/SL orders for this market
	ordersClient := account.NewOrdersClient(c.apiClient, c.cfg)
	// Get account index for GetOrders
	accountIndex := c.cfg.AccountIndex
	orders, err := ordersClient.GetOrders(ctx, &accountIndex, authToken) // Get all orders
	if err == nil {
		// Resolve market symbol to market ID for filtering
		marketID, err := markets.GetMarketIDGlobal(marketSymbol)
		if err == nil {
			for _, order := range orders {
				// Check if this order is for our market (compare MarketIndex with marketID)
				// MarketIndex is uint8, marketID is uint32, so we compare uint32(marketIndex) == marketID
				if uint32(order.MarketIndex) == marketID {
					orderTypeStr := strings.ToLower(order.OrderType)
					if orderTypeStr == "stop_loss" || orderTypeStr == "stop_loss_limit" ||
						orderTypeStr == "take_profit" || orderTypeStr == "take_profit_limit" {
						// Cancel this TP/SL order
						cancelHash, err := c.orderClient.CancelOrder(ctx, marketSymbol, int64(order.ClientOrderIndex), authToken)
						if err == nil {
							txHashes = append(txHashes, cancelHash)
						}
					}
				}
			}
		}
	}

	// Step 2: Close the position (always uses market order)
	closeHash, err := c.ClosePosition(ctx, marketSymbol, authToken)
	if err != nil {
		return txHashes, fmt.Errorf("failed to close position: %w", err)
	}
	txHashes = append(txHashes, closeHash)

	return txHashes, nil
}

// ReducePosition reduces a position by a percentage or maximum size
// This is a "reduce-only" order that will never exceed the current position size
// percentage: Percentage to reduce (0-100), e.g., 50 = reduce by 50%
// maxSize: Maximum size to reduce (0 = use percentage), takes precedence over percentage
// timeInForce: Time in force for the order (IOC for market orders, GTC for limit orders)
// Returns the transaction hash of the reduce order
func (c *PositionsClient) ReducePosition(ctx context.Context, marketSymbol string, orderType account.OrderType, price float64, percentage float64, maxSize float64, timeInForce account.TimeInForce, authToken string) (string, error) {
	// Get current position
	positions, err := c.ListPositions(ctx, marketSymbol, authToken)
	if err != nil {
		return "", fmt.Errorf("failed to get positions: %w", err)
	}

	// Find the position for this market
	var currentPosition *Position
	for _, pos := range positions {
		if pos.MarketSymbol == marketSymbol {
			currentPosition = pos
			break
		}
	}

	if currentPosition == nil {
		return "", fmt.Errorf("no position found for %s", marketSymbol)
	}

	// Get current position size (convert from *big.Float to float64)
	var currentSizeFloat float64
	if currentPosition.Size != nil {
		currentSizeFloat, _ = currentPosition.Size.Float64()
		// Take absolute value
		if currentSizeFloat < 0 {
			currentSizeFloat = -currentSizeFloat
		}
	}

	if currentSizeFloat == 0 {
		return "", fmt.Errorf("position size is zero for %s", marketSymbol)
	}

	// Fix: Position size is in ten-thousandths, convert to tokens
	currentSizeTokens := currentSizeFloat / 10_000

	// Calculate reduce size (maxSize and percentage are in tokens)
	var reduceSize float64
	if maxSize > 0 {
		// Use maxSize if specified (already in tokens)
		reduceSize = maxSize
		if reduceSize > currentSizeTokens {
			reduceSize = currentSizeTokens // Cap at current position size
			fmt.Fprintf(os.Stderr, "[DEBUG] Max size %.4f exceeds position size %.4f, capping to %.4f\n", maxSize, currentSizeTokens, reduceSize)
		}
	} else if percentage > 0 {
		// Use percentage if specified
		if percentage > 100 {
			percentage = 100 // Cap at 100%
		}
		reduceSize = currentSizeTokens * (percentage / 100.0)
		if reduceSize > currentSizeTokens {
			reduceSize = currentSizeTokens // Safety check
		}
	} else {
		return "", fmt.Errorf("either --percentage or --max-size must be specified")
	}

	if reduceSize <= 0 {
		return "", fmt.Errorf("calculated reduce size is zero or negative")
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Reducing position: current=%.4f tokens, reduce=%.4f tokens (%.2f%%)\n", currentSizeTokens, reduceSize, (reduceSize/currentSizeTokens)*100)

	// Determine side: if position is long (positive), we need to sell (reduce)
	// If position is short (negative), we need to buy (reduce)
	var side account.OrderSide
	var positionSizeFloat float64
	if currentPosition.Size != nil {
		positionSizeFloat, _ = currentPosition.Size.Float64()
	}
	if positionSizeFloat > 0 {
		// Long position -> sell to reduce
		side = account.OrderSideSell
	} else {
		// Short position -> buy to reduce
		side = account.OrderSideBuy
	}

	// Determine time-in-force: IOC for market orders, use provided TIF for limit orders
	tif := timeInForce
	if orderType == account.OrderTypeMarket {
		tif = account.TIFIOC // Market orders must be IOC
	} else if tif == "" {
		tif = account.TIFGTC // Default to GTC for limit orders if not specified
	}

	// Set order expiry based on time-in-force
	var orderExpiry int64
	if tif == account.TIFIOC {
		orderExpiry = 0 // No expiry for IOC
	} else {
		// Default expiry for GTC orders (1 hour from now)
		orderExpiry = time.Now().Add(1 * time.Hour).UnixMilli()
	}

	// Place the reduce order
	txHash, err := c.orderClient.PlaceOrder(
		ctx,
		marketSymbol,
		side,
		orderType,
		price,
		reduceSize,
		0,        // Auto-generate client order index
		tif,      // Use specified time-in-force
		orderExpiry, // Order expiry
		0,        // No trigger price
		authToken,
	)
	if err != nil {
		return "", fmt.Errorf("failed to place reduce order: %w", err)
	}

	return txHash, nil
}

// parsePositions parses positions from account data
func parsePositions(positionsData map[string]interface{}) ([]*Position, error) {
	if positionsData == nil {
		return []*Position{}, nil
	}

	var positions []*Position

	// Positions are stored as map[string]interface{} with keys like "position_0", "position_1", etc.
	for key, posData := range positionsData {
		// Skip non-position keys
		if strings.HasPrefix(key, "position_") {
			if posMap, ok := posData.(map[string]interface{}); ok {
				position, err := parsePosition(posMap)
				if err != nil {
					// Log error but continue parsing other positions
					continue
				}
				if position != nil {
					positions = append(positions, position)
				}
			}
		}
	}

	return positions, nil
}

// parsePosition parses a single position from position data
func parsePosition(posMap map[string]interface{}) (*Position, error) {
	position := &Position{}

	// Debug: log all available fields
	fmt.Fprintf(os.Stderr, "[DEBUG] Parsing position with fields: %v\n", getMapKeys(posMap))

	// Parse market_id (try multiple field names)
	var marketID uint32
	if id, ok := posMap["market_id"].(float64); ok {
		marketID = uint32(id)
	} else if id, ok := posMap["marketId"].(float64); ok {
		marketID = uint32(id)
	} else if id, ok := posMap["market_index"].(float64); ok {
		marketID = uint32(id)
	} else if id, ok := posMap["marketIndex"].(float64); ok {
		marketID = uint32(id)
	} else {
		// Try to infer from market symbol if present
		if marketSymbol, ok := posMap["market"].(string); ok {
			if id, err := markets.GetMarketIDGlobal(marketSymbol); err == nil {
				marketID = id
			} else {
				return nil, fmt.Errorf("position missing market_id and could not resolve market symbol")
			}
		} else {
			return nil, fmt.Errorf("position missing market_id")
		}
	}

	position.MarketID = marketID
	// Try to get market symbol
	if symbol, err := markets.GetMarketSymbolGlobal(position.MarketID); err == nil {
		position.MarketSymbol = symbol
	} else {
		position.MarketSymbol = fmt.Sprintf("market_%d", position.MarketID)
	}

	// Parse size (the field is called "position" in the API response)
	// Also check "sign" field - positive = long, negative = short
	var sign float64 = 1.0
	if signVal, ok := posMap["sign"].(float64); ok {
		sign = signVal
	} else if signVal, ok := posMap["sign"].(string); ok {
		if signVal == "-1" || signVal == "-" {
			sign = -1.0
		}
	}

	// Try "position" field first (this is the actual position size)
	if positionStr, ok := posMap["position"].(string); ok && positionStr != "" {
		size := new(big.Float)
		if _, _, err := size.Parse(positionStr, 10); err == nil {
			// Apply sign to determine long/short
			size.Mul(size, big.NewFloat(sign))
			position.Size = size
		}
	} else if positionFloat, ok := posMap["position"].(float64); ok && positionFloat != 0 {
		// Apply sign
		position.Size = big.NewFloat(positionFloat * sign)
	} else if sizeStr, ok := posMap["size"].(string); ok && sizeStr != "" {
		// Fallback to "size" field
		size := new(big.Float)
		if _, _, err := size.Parse(sizeStr, 10); err == nil {
			size.Mul(size, big.NewFloat(sign))
			position.Size = size
		}
	} else if sizeFloat, ok := posMap["size"].(float64); ok && sizeFloat != 0 {
		position.Size = big.NewFloat(sizeFloat * sign)
	} else if baseAmount, ok := posMap["base_amount"].(string); ok && baseAmount != "" {
		// Try base_amount as alternative (in micro-tokens)
		size := new(big.Float)
		if _, _, err := size.Parse(baseAmount, 10); err == nil {
			// Convert from micro-tokens to tokens (divide by 1,000,000)
			size.Quo(size, big.NewFloat(1_000_000))
			size.Mul(size, big.NewFloat(sign))
			position.Size = size
		}
	} else if baseAmount, ok := posMap["base_amount"].(float64); ok && baseAmount != 0 {
		// Convert from micro-tokens to tokens
		position.Size = big.NewFloat((baseAmount / 1_000_000) * sign)
	}

	// Parse entry_price (try avg_entry_price as well)
	if entryPriceStr, ok := posMap["entry_price"].(string); ok {
		price := new(big.Float)
		if _, _, err := price.Parse(entryPriceStr, 10); err == nil {
			// Convert from micro-USDC to USDC (divide by 1,000,000)
			price.Quo(price, big.NewFloat(1_000_000))
			position.EntryPrice = price
		}
	} else if entryPriceFloat, ok := posMap["entry_price"].(float64); ok {
		position.EntryPrice = big.NewFloat(entryPriceFloat / 1_000_000)
	} else if avgEntryPriceStr, ok := posMap["avg_entry_price"].(string); ok {
		price := new(big.Float)
		if _, _, err := price.Parse(avgEntryPriceStr, 10); err == nil {
			price.Quo(price, big.NewFloat(1_000_000))
			position.EntryPrice = price
		}
	} else if avgEntryPriceFloat, ok := posMap["avg_entry_price"].(float64); ok {
		position.EntryPrice = big.NewFloat(avgEntryPriceFloat / 1_000_000)
	} else if entryPrice, ok := posMap["entryPrice"].(float64); ok {
		position.EntryPrice = big.NewFloat(entryPrice / 1_000_000)
	}

	// Parse mark_price
	if markPriceStr, ok := posMap["mark_price"].(string); ok {
		price := new(big.Float)
		if _, _, err := price.Parse(markPriceStr, 10); err == nil {
			price.Quo(price, big.NewFloat(1_000_000))
			position.MarkPrice = price
		}
	} else if markPriceFloat, ok := posMap["mark_price"].(float64); ok {
		position.MarkPrice = big.NewFloat(markPriceFloat / 1_000_000)
	} else if markPrice, ok := posMap["markPrice"].(float64); ok {
		position.MarkPrice = big.NewFloat(markPrice / 1_000_000)
	}

	// Parse liquidation_price
	if liqPriceStr, ok := posMap["liquidation_price"].(string); ok {
		price := new(big.Float)
		if _, _, err := price.Parse(liqPriceStr, 10); err == nil {
			price.Quo(price, big.NewFloat(1_000_000))
			position.LiquidationPrice = price
		}
	} else if liqPriceFloat, ok := posMap["liquidation_price"].(float64); ok {
		position.LiquidationPrice = big.NewFloat(liqPriceFloat / 1_000_000)
	} else if liqPrice, ok := posMap["liquidationPrice"].(float64); ok {
		position.LiquidationPrice = big.NewFloat(liqPrice / 1_000_000)
	}

	// Parse unrealized_pnl (already in USDC, not micro-USDC based on API responses)
	if pnlStr, ok := posMap["unrealized_pnl"].(string); ok {
		pnl := new(big.Float)
		if _, _, err := pnl.Parse(pnlStr, 10); err == nil {
			// Check if it's already in USDC or needs conversion
			// Based on API responses, it appears to be in USDC already
			position.UnrealizedPnl = pnl
		}
	} else if pnlFloat, ok := posMap["unrealized_pnl"].(float64); ok {
		position.UnrealizedPnl = big.NewFloat(pnlFloat)
	} else if pnl, ok := posMap["unrealizedPnl"].(float64); ok {
		position.UnrealizedPnl = big.NewFloat(pnl)
	}

	// Parse margin_used (try allocated_margin as well)
	if marginStr, ok := posMap["margin_used"].(string); ok {
		margin := new(big.Float)
		if _, _, err := margin.Parse(marginStr, 10); err == nil {
			// Check if it's already in USDC or needs conversion
			position.MarginUsed = margin
		}
	} else if marginFloat, ok := posMap["margin_used"].(float64); ok {
		position.MarginUsed = big.NewFloat(marginFloat)
	} else if allocatedMarginStr, ok := posMap["allocated_margin"].(string); ok {
		margin := new(big.Float)
		if _, _, err := margin.Parse(allocatedMarginStr, 10); err == nil {
			position.MarginUsed = margin
		}
	} else if allocatedMarginFloat, ok := posMap["allocated_margin"].(float64); ok {
		position.MarginUsed = big.NewFloat(allocatedMarginFloat)
	} else if margin, ok := posMap["marginUsed"].(float64); ok {
		position.MarginUsed = big.NewFloat(margin)
	}

	// Parse leverage
	if leverage, ok := posMap["leverage"].(float64); ok {
		position.Leverage = leverage
	}

	// Store raw data
	if rawData, err := json.Marshal(posMap); err == nil {
		position.Raw = rawData
	}

	// Only return position if it has a non-zero size
	if position.Size != nil {
		sizeFloat, _ := position.Size.Float64()
		if sizeFloat == 0 {
			return nil, nil // Skip zero-size positions
		}
	} else {
		// If no size found, still return position but log warning
		fmt.Fprintf(os.Stderr, "[WARNING] Position for %s has no size data\n", position.MarketSymbol)
		return nil, nil // Skip positions without size
	}

	return position, nil
}

// getMapKeys returns all keys from a map for debugging
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// FormatSize formats position size as string
func (p *Position) FormatSize() string {
	if p.Size == nil {
		return "0"
	}
	sizeFloat, _ := p.Size.Float64()
	if sizeFloat >= 0 {
		return fmt.Sprintf("%.4f", sizeFloat)
	}
	return fmt.Sprintf("%.4f", sizeFloat)
}

// FormatEntryPrice formats entry price as string
func (p *Position) FormatEntryPrice() string {
	if p.EntryPrice == nil {
		return "N/A"
	}
	priceFloat, _ := p.EntryPrice.Float64()
	return fmt.Sprintf("%.2f", priceFloat)
}

// FormatMarkPrice formats mark price as string
func (p *Position) FormatMarkPrice() string {
	if p.MarkPrice == nil {
		return "N/A"
	}
	priceFloat, _ := p.MarkPrice.Float64()
	return fmt.Sprintf("%.2f", priceFloat)
}

// FormatUnrealizedPnl formats unrealized P&L as string
func (p *Position) FormatUnrealizedPnl() string {
	if p.UnrealizedPnl == nil {
		return "N/A"
	}
	pnlFloat, _ := p.UnrealizedPnl.Float64()
	if pnlFloat >= 0 {
		return fmt.Sprintf("+%.2f", pnlFloat)
	}
	return fmt.Sprintf("%.2f", pnlFloat)
}

