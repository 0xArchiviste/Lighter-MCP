package markets

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

//go:embed default_markets.json
var embeddedMarketsJSON []byte

// MarketResolver resolves market symbols to market IDs (bidirectional)
type MarketResolver struct {
	symbolToID map[string]uint32 // symbol -> market_id (case-insensitive key)
	idToSymbol map[uint32]string // market_id -> symbol (for reverse lookup)
}

// MarketEntry represents a market entry from markets.json
type MarketEntry struct {
	Name     string `json:"name"`
	MarketID uint32 `json:"market_id"`
}

// MarketsFile represents the markets.json structure
type MarketsFile struct {
	Count    int           `json:"count"`
	Markets  []MarketEntry `json:"markets"`
	Timestamp string       `json:"timestamp,omitempty"`
}

var globalResolver *MarketResolver

func parseMarketsData(data []byte) (*MarketResolver, error) {
	var marketsFile MarketsFile
	if err := json.Unmarshal(data, &marketsFile); err != nil {
		return nil, fmt.Errorf("failed to parse markets.json: %w", err)
	}

	resolver := &MarketResolver{
		symbolToID: make(map[string]uint32),
		idToSymbol: make(map[uint32]string),
	}

	for _, market := range marketsFile.Markets {
		upperSymbol := strings.ToUpper(market.Name)
		resolver.symbolToID[upperSymbol] = market.MarketID
		resolver.idToSymbol[market.MarketID] = market.Name
	}

	return resolver, nil
}

// LoadMarketResolver loads market mappings from a JSON file and sets the global resolver.
func LoadMarketResolver(filePath string) (*MarketResolver, error) {
	if filePath == "" {
		filePath = "markets.json"
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read markets.json: %w", err)
	}

	resolver, err := parseMarketsData(data)
	if err != nil {
		return nil, err
	}

	globalResolver = resolver
	return resolver, nil
}

// ensureGlobalResolver loads symbol→market_id mappings from, in order:
// LIGHTER_MARKETS_JSON (file path), ./markets.json in the process working directory, or the embedded snapshot.
func ensureGlobalResolver() error {
	if globalResolver != nil {
		return nil
	}
	if p := strings.TrimSpace(os.Getenv("LIGHTER_MARKETS_JSON")); p != "" {
		_, err := LoadMarketResolver(p)
		return err
	}
	if _, err := os.Stat("markets.json"); err == nil {
		_, err := LoadMarketResolver("markets.json")
		return err
	}
	r, err := parseMarketsData(embeddedMarketsJSON)
	if err != nil {
		return err
	}
	globalResolver = r
	return nil
}

// GetMarketID gets market ID for a symbol (case-insensitive)
func (r *MarketResolver) GetMarketID(symbol string) (uint32, error) {
	upperSymbol := strings.ToUpper(strings.TrimSpace(symbol))
	marketID, exists := r.symbolToID[upperSymbol]
	if !exists {
		return 0, fmt.Errorf("market not found: %s", symbol)
	}
	return marketID, nil
}

// GetMarketIDGlobal gets market ID using the global resolver (loads if needed)
func GetMarketIDGlobal(symbol string) (uint32, error) {
	if err := ensureGlobalResolver(); err != nil {
		return 0, err
	}
	return globalResolver.GetMarketID(symbol)
}

// GetMarketSymbol gets market symbol for a market ID
func (r *MarketResolver) GetMarketSymbol(marketID uint32) (string, error) {
	symbol, exists := r.idToSymbol[marketID]
	if !exists {
		return "", fmt.Errorf("market ID not found: %d", marketID)
	}
	return symbol, nil
}

// GetMarketSymbolGlobal gets market symbol using the global resolver (loads if needed)
func GetMarketSymbolGlobal(marketID uint32) (string, error) {
	if err := ensureGlobalResolver(); err != nil {
		return "", err
	}
	return globalResolver.GetMarketSymbol(marketID)
}

// GetAllMarkets returns all market mappings (symbol -> ID)
func (r *MarketResolver) GetAllMarkets() map[string]uint32 {
	result := make(map[string]uint32)
	for k, v := range r.symbolToID {
		result[k] = v
	}
	return result
}

// SaveMarketResolver saves market mappings to markets.json
func SaveMarketResolver(filePath string, markets []Market) error {
	if filePath == "" {
		filePath = "markets.json"
	}

	// Convert Market slice to MarketEntry slice
	entries := make([]MarketEntry, 0, len(markets))
	for _, market := range markets {
		if market.Name != "" {
			entries = append(entries, MarketEntry{
				Name:     market.Name,
				MarketID: market.MarketID,
			})
		}
	}

	marketsFile := MarketsFile{
		Count:     len(entries),
		Markets:   entries,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(marketsFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal markets.json: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write markets.json: %w", err)
	}

	return nil
}

// InitializeMarketResolver initializes the market resolver, creating markets.json if it doesn't exist
func InitializeMarketResolver(filePath string, marketsClient *Client, authToken string) error {
	if filePath == "" {
		filePath = "markets.json"
	}

	// Try to load existing markets.json
	resolver, err := LoadMarketResolver(filePath)
	if err == nil {
		// Successfully loaded, set as global
		globalResolver = resolver
		return nil
	}

	// If file doesn't exist, fetch markets from API and create it
	if os.IsNotExist(err) {
		ctx := context.Background()
		markets, fetchErr := marketsClient.GetMarkets(ctx, authToken)
		if fetchErr != nil {
			return fmt.Errorf("failed to fetch markets to create markets.json: %w", fetchErr)
		}

		if len(markets) == 0 {
			return fmt.Errorf("no markets found to save")
		}

		// Save markets.json
		if saveErr := SaveMarketResolver(filePath, markets); saveErr != nil {
			return fmt.Errorf("failed to save markets.json: %w", saveErr)
		}

		// Now load it
		resolver, loadErr := LoadMarketResolver(filePath)
		if loadErr != nil {
			return fmt.Errorf("failed to load newly created markets.json: %w", loadErr)
		}

		globalResolver = resolver
		return nil
	}

	// Other error (permission, etc.)
	return fmt.Errorf("failed to load markets.json: %w", err)
}

