package account

import (
	"context"
	"fmt"
	"time"
)

// ScaleOrderConfig defines parameters for a scale order
type ScaleOrderConfig struct {
	MarketSymbol     string
	Side             OrderSide
	TotalSize        float64   // Total size to trade
	StartPrice       float64   // Starting price
	EndPrice         float64   // Ending price
	NumOrders        int       // Number of orders to split into
	OrderType        OrderType // Usually limit
	TimeInForce      TimeInForce
	DelayBetweenOrders time.Duration // Optional delay between placing orders
}

// PlaceScaleOrder places multiple orders at different price levels
// This distributes a large order across multiple price points
// Returns array of transaction hashes for each order placed
func (c *OrderPlacementClient) PlaceScaleOrder(ctx context.Context, config ScaleOrderConfig, authToken string) ([]string, error) {
	if config.NumOrders <= 0 {
		return nil, fmt.Errorf("num_orders must be > 0")
	}
	if config.TotalSize <= 0 {
		return nil, fmt.Errorf("total_size must be > 0")
	}
	if config.NumOrders == 1 {
		return nil, fmt.Errorf("use PlaceOrder for single orders, scale orders require num_orders > 1")
	}

	// Validate price range
	if config.Side == OrderSideBuy {
		// For buy orders, start price should be higher than end price (buying as price drops)
		if config.StartPrice < config.EndPrice {
			return nil, fmt.Errorf("for buy orders, start_price should be >= end_price")
		}
	} else {
		// For sell orders, start price should be lower than end price (selling as price rises)
		if config.StartPrice > config.EndPrice {
			return nil, fmt.Errorf("for sell orders, start_price should be <= end_price")
		}
	}

	// Calculate size per order
	sizePerOrder := config.TotalSize / float64(config.NumOrders)
	
	// Calculate price increment
	priceIncrement := (config.EndPrice - config.StartPrice) / float64(config.NumOrders-1)

	var txHashes []string
	var errors []error

	// Place orders at each price level
	for i := 0; i < config.NumOrders; i++ {
		orderPrice := config.StartPrice + (float64(i) * priceIncrement)
		
		// Place order
		txHash, err := c.PlaceOrder(
			ctx,
			config.MarketSymbol,
			config.Side,
			config.OrderType,
			orderPrice,
			sizePerOrder,
			0, // Auto-generate client order index for each
			config.TimeInForce,
			0, // Default expiry
			0, // No trigger price for scale orders
			authToken,
		)
		
		if err != nil {
			errors = append(errors, fmt.Errorf("order %d at price %.2f: %w", i+1, orderPrice, err))
			continue
		}
		
		txHashes = append(txHashes, txHash)
		
		// Optional delay between orders to avoid rate limits
		if config.DelayBetweenOrders > 0 && i < config.NumOrders-1 {
			time.Sleep(config.DelayBetweenOrders)
		}
	}

	if len(errors) > 0 {
		// Return partial success with error
		return txHashes, fmt.Errorf("placed %d/%d orders, errors: %v", len(txHashes), config.NumOrders, errors[0])
	}

	return txHashes, nil
}

// CancelScaleOrders cancels all orders in a price range for a market
// This is useful for cancelling a scale order
func (c *OrderPlacementClient) CancelScaleOrders(ctx context.Context, marketSymbol string, minPrice, maxPrice float64, authToken string) ([]string, error) {
	// This would need to:
	// 1. List all open orders for the market
	// 2. Filter orders in the price range
	// 3. Cancel each order
	// For now, return an error suggesting to use CancelAllOrdersForMarket
	return nil, fmt.Errorf("cancel scale orders not yet implemented - use orders cancel --market %s --all to cancel all orders for the market", marketSymbol)
}



