package markets

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/0xarchiviste/lighter-mcp/pkg/api"
	"github.com/0xarchiviste/lighter-mcp/pkg/ws"
)

// Market represents a trading market with price information
type Market struct {
	Name           string
	MarketID       uint32
	CurrentPrice   *big.Float
	BidPrice       *big.Float
	AskPrice       *big.Float
	Volume24h      *big.Float
	PriceChange24h *big.Float
	OpenInterest   *big.Float
	FundingRate    *big.Float
	LastTradePrice *big.Float
	DailyHigh      *big.Float
	DailyLow       *big.Float
}

// Client provides market-related operations
type Client struct {
	apiClient *api.Client
}

// NewClient creates a new markets client
func NewClient(apiClient *api.Client) *Client {
	return &Client{
		apiClient: apiClient,
	}
}

// GetMarket retrieves a specific market by ID or name
func (c *Client) GetMarket(ctx context.Context, identifier string, authToken string) (*Market, error) {
	// First, get all markets
	marketsList, err := c.GetMarkets(ctx, authToken)
	if err != nil {
		return nil, err
	}

	// Try to find by name first (exact match)
	for _, market := range marketsList {
		if market.Name == identifier {
			return &market, nil
		}
	}

	// Try partial match (e.g., "ETH" matches "ETH-USD")
	for _, market := range marketsList {
		if market.Name == identifier || 
		   strings.HasPrefix(market.Name, identifier+"-") ||
		   strings.HasPrefix(market.Name, identifier+"_") {
			return &market, nil
		}
	}

	// Try to parse as market ID (uint32)
	// Handle formats like "0", "market_0", "0x0"
	var marketID uint32
	if _, err := fmt.Sscanf(identifier, "market_%d", &marketID); err == nil {
		// Found format "market_0"
	} else if _, err := fmt.Sscanf(identifier, "%d", &marketID); err == nil {
		// Found format "0"
	} else {
		return nil, fmt.Errorf("market not found: %s", identifier)
	}

	// Find by market ID
	for _, market := range marketsList {
		if market.MarketID == marketID {
			return &market, nil
		}
	}

	return nil, fmt.Errorf("market not found: %s", identifier)
}

// GetMarkets retrieves the list of available markets
func (c *Client) GetMarkets(ctx context.Context, authToken string) ([]Market, error) {
	// Try markets endpoint first
	rawData, err := c.apiClient.Markets(ctx, authToken)
	if err != nil {
		// If markets endpoint fails, try extracting markets from orderbooks
		return c.getMarketsFromOrderbooks(ctx, authToken)
	}

	// Parse markets response
	var markets []Market
	var responseMap map[string]interface{}
	if err := json.Unmarshal(rawData, &responseMap); err == nil {
		// Try to extract markets from various response formats
		if data, ok := responseMap["data"].([]interface{}); ok {
			markets = parseMarketsFromArray(data)
		} else if marketsArray, ok := responseMap["markets"].([]interface{}); ok {
			markets = parseMarketsFromArray(marketsArray)
		}
	} else {
		// Try parsing as array directly
		var marketsArray []interface{}
		if err := json.Unmarshal(rawData, &marketsArray); err == nil {
			markets = parseMarketsFromArray(marketsArray)
		}
	}

	if len(markets) == 0 {
		// Fallback to orderbooks if markets endpoint returned empty
		return c.getMarketsFromOrderbooks(ctx, authToken)
	}

	return markets, nil
}

// getMarketsFromOrderbooks extracts market list from orderbooks endpoint
func (c *Client) getMarketsFromOrderbooks(ctx context.Context, authToken string) ([]Market, error) {
	// Try to get orderbooks - this returns a map of market -> orderbook data
	orderbooksData, err := c.apiClient.OrderBooks(ctx, authToken)
	if err != nil {
		// If orderbooks endpoint also fails, try discovering markets by attempting common market names
		discovered, discoverErr := c.discoverMarketsByTrying(ctx, authToken)
		if discoverErr == nil && len(discovered) > 0 {
			return discovered, nil
		}
		// If discovery also fails, combine both errors for better debugging
		if discoverErr != nil {
			return nil, fmt.Errorf("failed to fetch markets: orderbooks endpoint failed (%v), and market discovery also failed (%v)", err, discoverErr)
		}
		// If discovery returned no error but also no markets, return the original orderbooks error
		return nil, fmt.Errorf("failed to fetch markets from orderbooks: %w", err)
	}

	// Parse orderbooks response - Python SDK format: { "code": 200, "order_books": [...] }
	var responseMap map[string]interface{}
	if err := json.Unmarshal(orderbooksData, &responseMap); err == nil {
		// Check for order_books array in response
		if orderBooksArray, ok := responseMap["order_books"].([]interface{}); ok {
			markets := make([]Market, 0, len(orderBooksArray))
			for _, item := range orderBooksArray {
				if obMap, ok := item.(map[string]interface{}); ok {
					market := Market{}
					// Extract symbol (market name) from orderbook object
					if symbol, ok := obMap["symbol"].(string); ok {
						market.Name = symbol
					} else if marketName, ok := obMap["market"].(string); ok {
						market.Name = marketName
					} else if marketName, ok := obMap["name"].(string); ok {
						market.Name = marketName
					}
					// Extract market_id
					if marketID, ok := obMap["market_id"].(float64); ok {
						market.MarketID = uint32(marketID)
					}
					if market.Name != "" {
						markets = append(markets, market)
					}
				}
			}
			if len(markets) > 0 {
				return markets, nil
			}
		}
		
		// Fallback: if response is a map, try extracting markets from keys (excluding metadata keys)
		markets := make([]Market, 0)
		for key, value := range responseMap {
			// Skip metadata keys like "code"
			if key == "code" || key == "message" || key == "error" {
				continue
			}
			// If value is an array, it might be orderbooks
			if arr, ok := value.([]interface{}); ok {
				for _, item := range arr {
					if obMap, ok := item.(map[string]interface{}); ok {
						market := Market{}
						if symbol, ok := obMap["symbol"].(string); ok {
							market.Name = symbol
						}
						if marketID, ok := obMap["market_id"].(float64); ok {
							market.MarketID = uint32(marketID)
						}
						if market.Name != "" {
							markets = append(markets, market)
						}
					}
				}
			}
		}
		if len(markets) > 0 {
			return markets, nil
		}
	}

	// Try parsing as direct array of orderbook objects
	var orderbooksArray []interface{}
	if err := json.Unmarshal(orderbooksData, &orderbooksArray); err == nil {
		markets := make([]Market, 0, len(orderbooksArray))
		for _, item := range orderbooksArray {
			if obMap, ok := item.(map[string]interface{}); ok {
				market := Market{}
				if symbol, ok := obMap["symbol"].(string); ok {
					market.Name = symbol
				} else if marketName, ok := obMap["market"].(string); ok {
					market.Name = marketName
				} else if marketName, ok := obMap["name"].(string); ok {
					market.Name = marketName
				}
				if marketID, ok := obMap["market_id"].(float64); ok {
					market.MarketID = uint32(marketID)
				}
				if market.Name != "" {
					markets = append(markets, market)
				}
			}
		}
		if len(markets) > 0 {
			return markets, nil
		}
	}

	return nil, fmt.Errorf("failed to parse orderbooks response or no markets found")
}

// discoverMarketsByTrying attempts to discover markets by trying common market names
func (c *Client) discoverMarketsByTrying(ctx context.Context, authToken string) ([]Market, error) {
	// Common market names to try (limit to most common ones to avoid long delays)
	commonMarkets := []string{
		"ETH-USD", "BTC-USD", "SOL-USD", "BNB-USD", "XRP-USD",
		"ADA-USD", "AVAX-USD", "DOT-USD", "MATIC-USD", "LINK-USD",
	}

	discoveredMarkets := make([]Market, 0)
	
	// Create a context with timeout for each individual request
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	
	// Try each market name to see if it exists (limit to first few to avoid long delays)
	for i, marketName := range commonMarkets {
		if i >= 5 { // Only try first 5 to avoid long delays
			break
		}
		_, err := c.apiClient.OrderBookDetails(requestCtx, marketName, authToken)
		if err == nil {
			// Market exists - add it to the list
			discoveredMarkets = append(discoveredMarkets, Market{
				Name: marketName,
			})
		}
		// Don't wait for timeout if we found some markets
		if len(discoveredMarkets) > 0 && i >= 2 {
			break
		}
	}

	if len(discoveredMarkets) > 0 {
		return discoveredMarkets, nil
	}

	return nil, fmt.Errorf("failed to discover markets - tried %d common market names, none found. The markets/orderbooks endpoints may not be available on this API instance, or the endpoint structure may have changed. Account endpoint works, suggesting API is accessible but markets feature may not be implemented", len(commonMarkets))
}

// parseMarketsFromArray parses market data from an array of interfaces
func parseMarketsFromArray(data []interface{}) []Market {
	markets := make([]Market, 0, len(data))
	for _, item := range data {
		if marketStr, ok := item.(string); ok {
			// Simple string format - just market name
			markets = append(markets, Market{
				Name: marketStr,
			})
		} else if marketMap, ok := item.(map[string]interface{}); ok {
			market := Market{}
			
			// Extract name
			if name, ok := marketMap["name"].(string); ok {
				market.Name = name
			} else if symbol, ok := marketMap["symbol"].(string); ok {
				market.Name = symbol
			} else if marketName, ok := marketMap["market"].(string); ok {
				market.Name = marketName
			}
			
			// Extract market ID
			if id, ok := marketMap["id"].(float64); ok {
				market.MarketID = uint32(id)
			} else if id, ok := marketMap["market_id"].(float64); ok {
				market.MarketID = uint32(id)
			}
			
			markets = append(markets, market)
		}
	}
	return markets
}

// GetMarketsWithPrices retrieves markets with price information from REST API
// limit: if > 0, only fetch prices for the first 'limit' markets
func (c *Client) GetMarketsWithPrices(ctx context.Context, authToken string, limit ...int) ([]Market, error) {
	markets, err := c.GetMarkets(ctx, authToken)
	if err != nil {
		return nil, err
	}

	// Apply limit if provided
	maxMarkets := len(markets)
	if len(limit) > 0 && limit[0] > 0 && limit[0] < len(markets) {
		maxMarkets = limit[0]
		markets = markets[:maxMarkets]
	}

	// Fetch orderbook for each market to get prices
	// Add delay between requests to avoid rate limits (100ms delay)
	delay := 100 * time.Millisecond
	for i := range markets {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return markets, ctx.Err()
		default:
		}

		orderbook, err := c.apiClient.OrderBookDetails(ctx, markets[i].Name, authToken)
		if err != nil {
			// If rate limited (429), wait longer before continuing
			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit") {
				time.Sleep(1 * time.Second)
				continue
			}
			continue // Skip if orderbook fetch fails
		}

		// Parse orderbook to extract prices
		c.extractPricesFromOrderbook(orderbook, &markets[i])
		
		// Try to fetch bid/ask from orderBookOrders endpoint (requires market_id)
		if markets[i].MarketID != 0 || markets[i].Name != "" {
			c.extractBidAskFromOrderBookOrders(ctx, &markets[i], authToken)
		}
		
		// Add delay between requests (except for the last one)
		if i < len(markets)-1 {
			time.Sleep(delay)
		}
	}

	return markets, nil
}

// GetMarketWithPrices retrieves a single market with price information from REST API
// This is optimized for fetching a single market and is much faster than GetMarketsWithPrices
func (c *Client) GetMarketWithPrices(ctx context.Context, marketNameOrID string, authToken string) (*Market, error) {
	// First get the market to ensure we have the correct market_id
	market, err := c.GetMarket(ctx, marketNameOrID, authToken)
	if err != nil {
		return nil, err
	}

	// Fetch orderbook details for this specific market
	orderbook, err := c.apiClient.OrderBookDetails(ctx, market.Name, authToken)
	if err != nil {
		// If rate limited (429), wait and retry once
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit") {
			time.Sleep(1 * time.Second)
			orderbook, err = c.apiClient.OrderBookDetails(ctx, market.Name, authToken)
			if err != nil {
				return market, err // Return market without prices if orderbook fetch fails
			}
		} else {
			return market, err // Return market without prices if orderbook fetch fails
		}
	}

	// Parse orderbook to extract prices
	c.extractPricesFromOrderbook(orderbook, market)

	// Fetch bid/ask from orderBookOrders endpoint (market_id 0 is valid for ETH)
	c.extractBidAskFromOrderBookOrders(ctx, market, authToken)

	return market, nil
}

// extractPricesFromOrderBooksBulk extracts prices from OrderBooks bulk response
// Format: { "order_books": [{ "symbol": "ETH", "market_id": 0, ... }, ...] }
func (c *Client) extractPricesFromOrderBooksBulk(orderBooksData json.RawMessage, markets []Market) {
	var responseMap map[string]interface{}
	if err := json.Unmarshal(orderBooksData, &responseMap); err != nil {
		return
	}

	// Create a map for quick lookup
	marketMap := make(map[string]*Market)
	for i := range markets {
		marketMap[markets[i].Name] = &markets[i]
		// Also index by market_id as string
		marketMap[fmt.Sprintf("id_%d", markets[i].MarketID)] = &markets[i]
	}

	// Try order_books array first
	if orderBooksArray, ok := responseMap["order_books"].([]interface{}); ok {
		for _, obItem := range orderBooksArray {
			if obMap, ok := obItem.(map[string]interface{}); ok {
				c.matchAndExtractPrices(obMap, marketMap)
			}
		}
		return
	}

	// Try order_book_details array (same format as OrderBookDetails)
	if orderBookDetailsArray, ok := responseMap["order_book_details"].([]interface{}); ok {
		for _, obItem := range orderBookDetailsArray {
			if obMap, ok := obItem.(map[string]interface{}); ok {
				c.matchAndExtractPrices(obMap, marketMap)
			}
		}
		return
	}

	// Try direct object format
	if obData, ok := responseMap["order_books"].(map[string]interface{}); ok {
		c.matchAndExtractPrices(obData, marketMap)
	}
}

// matchAndExtractPrices matches an orderbook entry to a market and extracts prices
func (c *Client) matchAndExtractPrices(obMap map[string]interface{}, marketMap map[string]*Market) {
	var market *Market
	
	// Find market by symbol/name
	if symbol, ok := obMap["symbol"].(string); ok {
		if m, exists := marketMap[symbol]; exists {
			market = m
		}
	}
	
	// Or find by market_id
	if market == nil {
		if marketID, ok := obMap["market_id"].(float64); ok {
			key := fmt.Sprintf("id_%d", uint32(marketID))
			if m, exists := marketMap[key]; exists {
				market = m
			}
		}
	}

	if market != nil {
		// Extract prices from this orderbook entry
		c.extractPricesFromOrderBookDetail(obMap, market)
		c.extractBidAskFromResponse(obMap, market)
	}
}

// GetMarketsWithPricesWebSocket retrieves markets with prices from WebSocket
// Uses WebSocket for real-time bid/ask prices, REST for market stats (volume, 24h change, etc.)
func (c *Client) GetMarketsWithPricesWebSocket(ctx context.Context, authToken string, marketsList []Market) ([]Market, error) {
	// Fetch prices for only the provided markets (not all markets)
	// This respects the limit flag and avoids unnecessary API calls
	marketsWithStats := make([]Market, len(marketsList))
	copy(marketsWithStats, marketsList)

	// Fetch prices for each market individually with rate limiting
	delay := 100 * time.Millisecond
	for i := range marketsWithStats {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return marketsWithStats, ctx.Err()
		default:
		}

		orderbook, err := c.apiClient.OrderBookDetails(ctx, marketsWithStats[i].Name, authToken)
		if err != nil {
			// If rate limited (429), wait longer before continuing
			if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate limit") {
				time.Sleep(1 * time.Second)
				continue
			}
			continue // Skip if orderbook fetch fails
		}

		// Parse orderbook to extract prices
		c.extractPricesFromOrderbook(orderbook, &marketsWithStats[i])
		
		// Try to fetch bid/ask from orderBookOrders endpoint
		if marketsWithStats[i].MarketID != 0 || marketsWithStats[i].Name != "" {
			c.extractBidAskFromOrderBookOrders(ctx, &marketsWithStats[i], authToken)
		}
		
		// Add delay between requests (except for the last one)
		if i < len(marketsWithStats)-1 {
			time.Sleep(delay)
		}
	}

	// Create a map for quick lookup
	marketMap := make(map[string]*Market)
	for i := range marketsWithStats {
		marketMap[marketsWithStats[i].Name] = &marketsWithStats[i]
	}

	// Now try to enhance with WebSocket bid/ask prices
	baseURL := c.apiClient.BaseURL()
	wsClient := ws.NewClient(baseURL, authToken)
	
	// Connect to WebSocket (non-blocking, with timeout)
	wsCtx, wsCancel := context.WithTimeout(ctx, 5*time.Second)
	defer wsCancel()
	
	wsConnected := make(chan error, 1)
	go func() {
		wsConnected <- wsClient.Connect()
	}()

	select {
	case err := <-wsConnected:
		if err != nil {
			// WebSocket connection failed - return REST data only
			return marketsWithStats, nil
		}
		defer wsClient.Close()
	case <-wsCtx.Done():
		// WebSocket connection timeout - return REST data only
		return marketsWithStats, nil
	}

	// Create orderbook manager
	orderbookManager := ws.NewOrderbookManager(wsClient, c.apiClient)
	
	// Start orderbook manager (subscribes to orderbook channels)
	if err := orderbookManager.Start(ctx); err != nil {
		// If orderbook manager fails, return REST data
		return marketsWithStats, nil
	}

	// Subscribe to orderbooks for markets we care about
	if len(marketsList) > 0 {
		for _, market := range marketsList {
			if _, ok := marketMap[market.Name]; ok {
				// Try different subscription formats
				subscriptions := []map[string]interface{}{
					{"market": market.Name},
					{"symbol": market.Name},
					{"channel": "orderbook", "market": market.Name},
				}
				for _, sub := range subscriptions {
					handler := func(data json.RawMessage) error {
						return nil // OrderbookManager handles it internally
					}
					if err := wsClient.Subscribe("orderbook", sub, handler); err == nil {
						break
					}
				}
			}
		}
	}

	// Wait briefly for WebSocket orderbook data (shorter timeout since we already have REST data)
	timeout := time.After(3 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			// Timeout - return markets with REST stats, WebSocket bid/ask if available
			return c.enhanceWithWebSocketPrices(marketsWithStats, orderbookManager), nil
		case <-ticker.C:
			// Check if we got any WebSocket data
			hasWebSocketData := false
			for i := range marketsWithStats {
				ob, err := orderbookManager.GetOrderbook(marketsWithStats[i].Name)
				if err == nil && ob != nil {
					hasWebSocketData = true
					c.extractPricesFromWebSocketOrderbook(ob, &marketsWithStats[i])
				}
			}
			// If we got WebSocket data, return immediately
			if hasWebSocketData {
				return marketsWithStats, nil
			}
		case <-ctx.Done():
			return c.enhanceWithWebSocketPrices(marketsWithStats, orderbookManager), nil
		}
	}
}

// enhanceWithWebSocketPrices enhances REST market data with WebSocket bid/ask prices
func (c *Client) enhanceWithWebSocketPrices(marketsList []Market, orderbookManager *ws.OrderbookManager) []Market {
	for i := range marketsList {
		ob, err := orderbookManager.GetOrderbook(marketsList[i].Name)
		if err == nil && ob != nil {
			c.extractPricesFromWebSocketOrderbook(ob, &marketsList[i])
		}
	}
	return marketsList
}


// extractPricesFromWebSocketOrderbook extracts prices from WebSocket orderbook snapshot
func (c *Client) extractPricesFromWebSocketOrderbook(ob *ws.OrderbookSnapshot, market *Market) {
	// Extract best bid
	if len(ob.Bids) > 0 && len(ob.Bids[0]) >= 2 {
		if priceStr := ob.Bids[0][0]; priceStr != "" {
			if price, _, err := big.ParseFloat(priceStr, 10, 256, big.ToNearestEven); err == nil {
				market.BidPrice = price
			}
		}
	}

	// Extract best ask
	if len(ob.Asks) > 0 && len(ob.Asks[0]) >= 2 {
		if priceStr := ob.Asks[0][0]; priceStr != "" {
			if price, _, err := big.ParseFloat(priceStr, 10, 256, big.ToNearestEven); err == nil {
				market.AskPrice = price
			}
		}
	}

	// Calculate current price as mid-point
	if market.BidPrice != nil && market.AskPrice != nil {
		mid := new(big.Float).Add(market.BidPrice, market.AskPrice)
		mid.Quo(mid, big.NewFloat(2))
		market.CurrentPrice = mid
	} else if market.BidPrice != nil {
		market.CurrentPrice = market.BidPrice
	} else if market.AskPrice != nil {
		market.CurrentPrice = market.AskPrice
	}
}

// extractPricesFromOrderbook extracts price information from orderbook JSON
// Python SDK format: { "code": 200, "order_book_details": [{...}] }
func (c *Client) extractPricesFromOrderbook(orderbook json.RawMessage, market *Market) {
	var responseMap map[string]interface{}
	if err := json.Unmarshal(orderbook, &responseMap); err != nil {
		return
	}

	// Handle order_book_details array format (Python SDK format)
	if orderBookDetailsArray, ok := responseMap["order_book_details"].([]interface{}); ok && len(orderBookDetailsArray) > 0 {
		// Find the orderbook detail that matches our market (by symbol or market_id)
		for _, obItem := range orderBookDetailsArray {
			if obDetail, ok := obItem.(map[string]interface{}); ok {
				// Check if this orderbook matches our market
				matches := false
				
				// Match by symbol/name
				if symbol, ok := obDetail["symbol"].(string); ok {
					if symbol == market.Name {
						matches = true
					}
				}
				
				// Match by market_id
				if !matches {
					if marketID, ok := obDetail["market_id"].(float64); ok {
						if uint32(marketID) == market.MarketID {
							matches = true
						}
					}
				}
				
				// If no match criteria found, use first entry (backward compatibility)
				if matches || (len(orderBookDetailsArray) == 1) {
					c.extractPricesFromOrderBookDetail(obDetail, market)
					// Also extract bid/ask from this orderbook detail
					c.extractBidAskFromResponse(obDetail, market)
					return
				}
			}
		}
		
		// If no match found, fall back to first entry (shouldn't happen, but for safety)
		if obDetail, ok := orderBookDetailsArray[0].(map[string]interface{}); ok {
			c.extractPricesFromOrderBookDetail(obDetail, market)
			c.extractBidAskFromResponse(obDetail, market)
			return
		}
	}

	// Fallback: try direct object format
	if obData, ok := responseMap["order_book_details"].(map[string]interface{}); ok {
		c.extractPricesFromOrderBookDetail(obData, market)
		c.extractBidAskFromResponse(obData, market)
		return
	}

	// Legacy format: try direct fields on response
	c.extractPricesFromOrderBookDetail(responseMap, market)
	c.extractBidAskFromResponse(responseMap, market)
}

// extractBidAskFromResponse extracts bid/ask from a response map (can be from orderBookDetails or orderBookOrders)
func (c *Client) extractBidAskFromResponse(responseMap map[string]interface{}, market *Market) {
	// Extract bids
	if bids, ok := responseMap["bids"].([]interface{}); ok && len(bids) > 0 {
		if bid, ok := bids[0].([]interface{}); ok && len(bid) > 0 {
			if priceStr, ok := bid[0].(string); ok {
				if price, _, err := big.ParseFloat(priceStr, 10, 256, big.ToNearestEven); err == nil {
					market.BidPrice = price
				}
			} else if priceFloat, ok := bid[0].(float64); ok {
				market.BidPrice = big.NewFloat(priceFloat)
			}
		} else if bidMap, ok := bids[0].(map[string]interface{}); ok {
			if price, ok := bidMap["price"].(float64); ok {
				market.BidPrice = big.NewFloat(price)
			} else if priceStr, ok := bidMap["price"].(string); ok {
				if price, _, err := big.ParseFloat(priceStr, 10, 256, big.ToNearestEven); err == nil {
					market.BidPrice = price
				}
			}
		}
	}

	// Extract asks
	if asks, ok := responseMap["asks"].([]interface{}); ok && len(asks) > 0 {
		if ask, ok := asks[0].([]interface{}); ok && len(ask) > 0 {
			if priceStr, ok := ask[0].(string); ok {
				if price, _, err := big.ParseFloat(priceStr, 10, 256, big.ToNearestEven); err == nil {
					market.AskPrice = price
				}
			} else if priceFloat, ok := ask[0].(float64); ok {
				market.AskPrice = big.NewFloat(priceFloat)
			}
		} else if askMap, ok := asks[0].(map[string]interface{}); ok {
			if price, ok := askMap["price"].(float64); ok {
				market.AskPrice = big.NewFloat(price)
			} else if priceStr, ok := askMap["price"].(string); ok {
				if price, _, err := big.ParseFloat(priceStr, 10, 256, big.ToNearestEven); err == nil {
					market.AskPrice = price
				}
			}
		}
	}

	// Calculate current price as mid-point if we have both bid and ask but no current price
	if market.BidPrice != nil && market.AskPrice != nil && market.CurrentPrice == nil {
		mid := new(big.Float).Add(market.BidPrice, market.AskPrice)
		mid.Quo(mid, big.NewFloat(2))
		market.CurrentPrice = mid
	}
}

// extractPricesFromOrderBookDetail extracts prices from a single orderbook detail object
func (c *Client) extractPricesFromOrderBookDetail(obDetail map[string]interface{}, market *Market) {
	// Extract last trade price as current price
	if lastTrade, ok := obDetail["last_trade_price"].(float64); ok {
		market.LastTradePrice = big.NewFloat(lastTrade)
		market.CurrentPrice = big.NewFloat(lastTrade)
	} else if lastTradeStr, ok := obDetail["last_trade_price"].(string); ok {
		if lastTrade, _, err := big.ParseFloat(lastTradeStr, 10, 256, big.ToNearestEven); err == nil {
			market.LastTradePrice = lastTrade
			market.CurrentPrice = lastTrade
		}
	}

	// Extract 24h price change (as percentage)
	if change, ok := obDetail["daily_price_change"].(float64); ok {
		market.PriceChange24h = big.NewFloat(change)
	}

	// Extract 24h volume (use quote token volume)
	if volume, ok := obDetail["daily_quote_token_volume"].(float64); ok {
		market.Volume24h = big.NewFloat(volume)
	} else if volume, ok := obDetail["daily_base_token_volume"].(float64); ok {
		// Fallback to base token volume if quote volume not available
		market.Volume24h = big.NewFloat(volume)
	}

	// Extract open interest
	if oi, ok := obDetail["open_interest"].(float64); ok {
		market.OpenInterest = big.NewFloat(oi)
	}

	// Extract daily high/low
	if high, ok := obDetail["daily_price_high"].(float64); ok {
		market.DailyHigh = big.NewFloat(high)
	}
	if low, ok := obDetail["daily_price_low"].(float64); ok {
		market.DailyLow = big.NewFloat(low)
	}
}

// extractBidAskFromOrderBookOrders extracts bid/ask prices from orderBookOrders endpoint
func (c *Client) extractBidAskFromOrderBookOrders(ctx context.Context, market *Market, authToken string) {
	orderBookOrders, err := c.apiClient.OrderBookOrders(ctx, market.MarketID, authToken)
	if err != nil {
		// Silently fail - bid/ask are optional (endpoint might not exist or might be rate limited)
		return
	}

	var responseMap map[string]interface{}
	if err := json.Unmarshal(orderBookOrders, &responseMap); err != nil {
		return
	}

	// Use the shared extraction function
	c.extractBidAskFromResponse(responseMap, market)
	
	// Also check if response has a nested structure (e.g., {"data": {"bids": [...], "asks": [...]}})
	if data, ok := responseMap["data"].(map[string]interface{}); ok {
		c.extractBidAskFromResponse(data, market)
	}
	if result, ok := responseMap["result"].(map[string]interface{}); ok {
		c.extractBidAskFromResponse(result, market)
	}
	if orderbook, ok := responseMap["orderbook"].(map[string]interface{}); ok {
		c.extractBidAskFromResponse(orderbook, market)
	}
}

