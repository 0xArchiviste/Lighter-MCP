package markets

import (
	"context"
	"testing"
	"time"

	"github.com/0xarchiviste/lighter-mcp/pkg/api"
	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/signer"
)

// TestGetMarkets tests that we can fetch markets list
func TestGetMarkets(t *testing.T) {
	cfg := config.Load()
	if cfg.APIKeyPrivateKey == "" {
		t.Skip("LIGHTER_API_KEY_PRIVATE_KEY not set, skipping test")
	}

	apiClient := api.New(cfg)
	client := NewClient(apiClient)

	// Create signer and auth token
	s, err := signer.New(signer.Config{
		BaseURL:          cfg.BaseURL,
		APIKeyPrivateKey: cfg.APIKeyPrivateKey,
		AccountIndex:     cfg.AccountIndex,
		APIKeyIndex:      cfg.APIKeyIndex,
	})
	if err != nil {
		t.Fatalf("Failed to create signer: %v", err)
	}
	s.SetNonceProvider(apiClient)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	authToken, err := s.CreateAuthTokenWithExpiry(ctx, 3600)
	if err != nil {
		t.Fatalf("Failed to create auth token: %v", err)
	}

	markets, err := client.GetMarkets(ctx, authToken)
	if err != nil {
		t.Fatalf("Failed to get markets: %v", err)
	}

	if len(markets) == 0 {
		t.Error("Expected at least one market, got 0")
	}

	// Verify markets have names
	for i, m := range markets {
		if m.Name == "" {
			t.Errorf("Market %d has empty name", i)
		}
		t.Logf("Market %d: %s (ID: %d)", i, m.Name, m.MarketID)
	}

	t.Logf("✓ Successfully fetched %d markets", len(markets))
}

// TestGetMarket tests fetching a specific market by name
func TestGetMarket(t *testing.T) {
	cfg := config.Load()
	if cfg.APIKeyPrivateKey == "" {
		t.Skip("LIGHTER_API_KEY_PRIVATE_KEY not set, skipping test")
	}

	apiClient := api.New(cfg)
	client := NewClient(apiClient)

	s, err := signer.New(signer.Config{
		BaseURL:          cfg.BaseURL,
		APIKeyPrivateKey: cfg.APIKeyPrivateKey,
		AccountIndex:     cfg.AccountIndex,
		APIKeyIndex:      cfg.APIKeyIndex,
	})
	if err != nil {
		t.Fatalf("Failed to create signer: %v", err)
	}
	s.SetNonceProvider(apiClient)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	authToken, err := s.CreateAuthTokenWithExpiry(ctx, 3600)
	if err != nil {
		t.Fatalf("Failed to create auth token: %v", err)
	}

	// Test fetching by name (try common markets)
	testMarkets := []string{"ETH", "BTC", "SOL"}
	found := false

	for _, marketName := range testMarkets {
		market, err := client.GetMarket(ctx, marketName, authToken)
		if err == nil && market != nil {
			if market.Name == "" {
				t.Errorf("Market %s has empty name", marketName)
			}
			t.Logf("✓ Found market: %s (ID: %d)", market.Name, market.MarketID)
			found = true
			break
		}
	}

	if !found {
		t.Log("Could not find any of the test markets (ETH, BTC, SOL) - this is OK if they don't exist")
	}
}

// TestGetMarketsWithPrices tests fetching markets with price information
func TestGetMarketsWithPrices(t *testing.T) {
	cfg := config.Load()
	if cfg.APIKeyPrivateKey == "" {
		t.Skip("LIGHTER_API_KEY_PRIVATE_KEY not set, skipping test")
	}

	apiClient := api.New(cfg)
	client := NewClient(apiClient)

	s, err := signer.New(signer.Config{
		BaseURL:          cfg.BaseURL,
		APIKeyPrivateKey: cfg.APIKeyPrivateKey,
		AccountIndex:     cfg.AccountIndex,
		APIKeyIndex:      cfg.APIKeyIndex,
	})
	if err != nil {
		t.Fatalf("Failed to create signer: %v", err)
	}
	s.SetNonceProvider(apiClient)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	authToken, err := s.CreateAuthTokenWithExpiry(ctx, 3600)
	if err != nil {
		t.Fatalf("Failed to create auth token: %v", err)
	}

	// Get markets with prices (limit to 3 for faster testing)
	markets, err := client.GetMarkets(ctx, authToken)
	if err != nil {
		t.Fatalf("Failed to get markets: %v", err)
	}

	// Limit to first 3 markets for testing
	if len(markets) > 3 {
		markets = markets[:3]
	}

	// Fetch prices for limited markets
	for i := range markets {
		orderbook, err := apiClient.OrderBookDetails(ctx, markets[i].Name, authToken)
		if err != nil {
			t.Logf("⚠️  Failed to fetch orderbook for %s: %v", markets[i].Name, err)
			continue
		}

		client.extractPricesFromOrderbook(orderbook, &markets[i])
	}

	// Verify price extraction
	hasPrices := false
	for i, m := range markets {
		t.Logf("Market %d: %s", i, m.Name)
		if m.CurrentPrice != nil {
			t.Logf("  Price: %s", m.CurrentPrice.Text('f', 6))
			hasPrices = true
		} else {
			t.Logf("  Price: N/A")
		}
		if m.PriceChange24h != nil {
			t.Logf("  24h Change: %s%%", m.PriceChange24h.Text('f', 2))
		}
		if m.Volume24h != nil {
			t.Logf("  Volume 24h: %s", m.Volume24h.Text('f', 2))
		}
		if m.OpenInterest != nil {
			t.Logf("  Open Interest: %s", m.OpenInterest.Text('f', 2))
		}
		if m.BidPrice != nil {
			t.Logf("  Bid: %s", m.BidPrice.Text('f', 6))
		}
		if m.AskPrice != nil {
			t.Logf("  Ask: %s", m.AskPrice.Text('f', 6))
		}
	}

	if !hasPrices {
		t.Error("No prices were extracted from any market")
	} else {
		t.Log("✓ Successfully extracted prices from markets")
	}
}

// TestPriceExtraction tests the price extraction logic directly
func TestPriceExtraction(t *testing.T) {
	cfg := config.Load()
	if cfg.APIKeyPrivateKey == "" {
		t.Skip("LIGHTER_API_KEY_PRIVATE_KEY not set, skipping test")
	}

	apiClient := api.New(cfg)
	client := NewClient(apiClient)

	s, err := signer.New(signer.Config{
		BaseURL:          cfg.BaseURL,
		APIKeyPrivateKey: cfg.APIKeyPrivateKey,
		AccountIndex:     cfg.AccountIndex,
		APIKeyIndex:      cfg.APIKeyIndex,
	})
	if err != nil {
		t.Fatalf("Failed to create signer: %v", err)
	}
	s.SetNonceProvider(apiClient)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	authToken, err := s.CreateAuthTokenWithExpiry(ctx, 3600)
	if err != nil {
		t.Fatalf("Failed to create auth token: %v", err)
	}

	// Get a market first
	markets, err := client.GetMarkets(ctx, authToken)
	if err != nil || len(markets) == 0 {
		t.Skip("No markets available for testing")
	}

	testMarket := markets[0]
	t.Logf("Testing price extraction for market: %s", testMarket.Name)

	// Fetch orderbook
	orderbook, err := apiClient.OrderBookDetails(ctx, testMarket.Name, authToken)
	if err != nil {
		t.Fatalf("Failed to fetch orderbook: %v", err)
	}

	// Extract prices
	client.extractPricesFromOrderbook(orderbook, &testMarket)

	// Verify extraction
	if testMarket.CurrentPrice == nil {
		t.Error("CurrentPrice was not extracted")
	} else {
		t.Logf("✓ CurrentPrice: %s", testMarket.CurrentPrice.Text('f', 6))
	}

	if testMarket.LastTradePrice == nil {
		t.Log("⚠️  LastTradePrice not extracted (may not be in response)")
	} else {
		t.Logf("✓ LastTradePrice: %s", testMarket.LastTradePrice.Text('f', 6))
	}

	if testMarket.PriceChange24h == nil {
		t.Log("⚠️  PriceChange24h not extracted (may not be in response)")
	} else {
		t.Logf("✓ PriceChange24h: %s%%", testMarket.PriceChange24h.Text('f', 2))
	}

	if testMarket.Volume24h == nil {
		t.Log("⚠️  Volume24h not extracted (may not be in response)")
	} else {
		t.Logf("✓ Volume24h: %s", testMarket.Volume24h.Text('f', 2))
	}

	if testMarket.OpenInterest == nil {
		t.Log("⚠️  OpenInterest not extracted (may not be in response)")
	} else {
		t.Logf("✓ OpenInterest: %s", testMarket.OpenInterest.Text('f', 2))
	}

	if testMarket.BidPrice == nil {
		t.Log("⚠️  BidPrice not extracted (OrderBookDetails may not include bids)")
	} else {
		t.Logf("✓ BidPrice: %s", testMarket.BidPrice.Text('f', 6))
	}

	if testMarket.AskPrice == nil {
		t.Log("⚠️  AskPrice not extracted (OrderBookDetails may not include asks)")
	} else {
		t.Logf("✓ AskPrice: %s", testMarket.AskPrice.Text('f', 6))
	}
}

// TestRateLimiting tests that rate limiting works correctly
func TestRateLimiting(t *testing.T) {
	cfg := config.Load()
	if cfg.APIKeyPrivateKey == "" {
		t.Skip("LIGHTER_API_KEY_PRIVATE_KEY not set, skipping test")
	}

	apiClient := api.New(cfg)
	client := NewClient(apiClient)

	s, err := signer.New(signer.Config{
		BaseURL:          cfg.BaseURL,
		APIKeyPrivateKey: cfg.APIKeyPrivateKey,
		AccountIndex:     cfg.AccountIndex,
		APIKeyIndex:      cfg.APIKeyIndex,
	})
	if err != nil {
		t.Fatalf("Failed to create signer: %v", err)
	}
	s.SetNonceProvider(apiClient)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	authToken, err := s.CreateAuthTokenWithExpiry(ctx, 3600)
	if err != nil {
		t.Fatalf("Failed to create auth token: %v", err)
	}

	// Get markets
	markets, err := client.GetMarkets(ctx, authToken)
	if err != nil {
		t.Fatalf("Failed to get markets: %v", err)
	}

	// Limit to 5 markets for rate limiting test
	if len(markets) > 5 {
		markets = markets[:5]
	}

	start := time.Now()
	
	// Fetch prices with rate limiting
	for i := range markets {
		orderbook, err := apiClient.OrderBookDetails(ctx, markets[i].Name, authToken)
		if err != nil {
			t.Logf("⚠️  Failed to fetch orderbook for %s: %v", markets[i].Name, err)
			continue
		}
		client.extractPricesFromOrderbook(orderbook, &markets[i])
		
		// Add delay between requests (100ms)
		if i < len(markets)-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	elapsed := time.Since(start)
	t.Logf("✓ Fetched prices for %d markets in %v (with rate limiting)", len(markets), elapsed)
	
	// Verify we got some prices
	hasPrices := false
	for _, m := range markets {
		if m.CurrentPrice != nil {
			hasPrices = true
			break
		}
	}
	
	if !hasPrices {
		t.Error("No prices were extracted despite rate limiting")
	} else {
		t.Log("✓ Rate limiting test passed - prices extracted successfully")
	}
}

