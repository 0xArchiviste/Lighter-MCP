package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/0xarchiviste/lighter-mcp/pkg/api"
)

// OrderbookManager manages orderbook data from WebSocket
type OrderbookManager struct {
	client      *Client
	apiClient   *api.Client
	orderbooks  map[string]*OrderbookSnapshot
	orderbooksMu sync.RWMutex
	subscribed  map[string]bool
	subscribedMu sync.RWMutex
}

// NewOrderbookManager creates a new orderbook manager
func NewOrderbookManager(wsClient *Client, apiClient *api.Client) *OrderbookManager {
	return &OrderbookManager{
		client:     wsClient,
		apiClient:  apiClient,
		orderbooks: make(map[string]*OrderbookSnapshot),
		subscribed: make(map[string]bool),
	}
}

// Start starts listening for orderbook updates
func (m *OrderbookManager) Start(ctx context.Context) error {
	// Subscribe to all orderbooks
	handler := func(data json.RawMessage) error {
		return m.handleOrderbookUpdate(data)
	}

	// Try subscribing to different channels
	channels := []string{
		"orderbook",
		"orderbooks",
		"order_book",
		"order_books",
		"market_data",
		"all",
	}

	subscribed := false
	for _, channel := range channels {
		if err := m.client.Subscribe(channel, map[string]interface{}{
			"channel": channel,
		}, handler); err == nil {
			log.Printf("Subscribed to orderbook channel: %s", channel)
			subscribed = true
			break
		}
	}

	if !subscribed {
		log.Println("Warning: Failed to subscribe to orderbook WebSocket, will use REST API fallback")
	}

	// Fallback: fetch orderbooks via REST API periodically if WebSocket fails
	go m.restFallback(ctx)

	return nil
}

// handleOrderbookUpdate handles incoming orderbook updates
func (m *OrderbookManager) handleOrderbookUpdate(data json.RawMessage) error {
	var snapshot OrderbookSnapshot
	if err := json.Unmarshal(data, &snapshot); err == nil {
		if snapshot.Market != "" {
			m.orderbooksMu.Lock()
			m.orderbooks[snapshot.Market] = &snapshot
			m.orderbooksMu.Unlock()
			return nil
		}
	}

	// Try parsing as map of markets
	var markets map[string]OrderbookSnapshot
	if err := json.Unmarshal(data, &markets); err == nil {
		m.orderbooksMu.Lock()
		for market, snapshot := range markets {
			m.orderbooks[market] = &snapshot
		}
		m.orderbooksMu.Unlock()
		return nil
	}

	// Try parsing as array
	var snapshots []OrderbookSnapshot
	if err := json.Unmarshal(data, &snapshots); err == nil {
		m.orderbooksMu.Lock()
		for _, snapshot := range snapshots {
			if snapshot.Market != "" {
				m.orderbooks[snapshot.Market] = &snapshot
			}
		}
		m.orderbooksMu.Unlock()
		return nil
	}

	return fmt.Errorf("failed to parse orderbook update")
}

// restFallback fetches orderbooks via REST API as fallback
func (m *OrderbookManager) restFallback(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Only use REST if WebSocket is not connected or no data received
			if !m.client.IsConnected() || len(m.orderbooks) == 0 {
				// Fetch markets first
				markets, err := m.apiClient.Markets(ctx, "")
				if err != nil {
					continue
				}

				// Parse markets and fetch orderbooks
				var marketList []string
				var marketsData interface{}
				if err := json.Unmarshal(markets, &marketsData); err == nil {
					if marketsSlice, ok := marketsData.([]interface{}); ok {
						for _, item := range marketsSlice {
							if marketStr, ok := item.(string); ok {
								marketList = append(marketList, marketStr)
							} else if marketMap, ok := item.(map[string]interface{}); ok {
								if name, ok := marketMap["name"].(string); ok {
									marketList = append(marketList, name)
								} else if symbol, ok := marketMap["symbol"].(string); ok {
									marketList = append(marketList, symbol)
								}
							}
						}
					}
				}

				// Fetch orderbook for each market (limit to avoid rate limits)
				for i, market := range marketList {
					if i >= 10 { // Limit to 10 markets
						break
					}
					orderbook, err := m.apiClient.OrderBookDetails(ctx, market, "")
					if err == nil {
						var obData OrderbookSnapshot
						if err := json.Unmarshal(orderbook, &obData); err == nil {
							obData.Market = market
							m.orderbooksMu.Lock()
							m.orderbooks[market] = &obData
							m.orderbooksMu.Unlock()
						}
					}
				}
			}
		}
	}
}

// GetOrderbook gets orderbook for a specific market
func (m *OrderbookManager) GetOrderbook(market string) (*OrderbookSnapshot, error) {
	m.orderbooksMu.RLock()
	defer m.orderbooksMu.RUnlock()

	ob, exists := m.orderbooks[market]
	if !exists {
		return nil, fmt.Errorf("orderbook not found for market: %s", market)
	}

	return ob, nil
}

// GetAllOrderbooks gets all orderbooks
func (m *OrderbookManager) GetAllOrderbooks() map[string]*OrderbookSnapshot {
	m.orderbooksMu.RLock()
	defer m.orderbooksMu.RUnlock()

	result := make(map[string]*OrderbookSnapshot)
	for market, ob := range m.orderbooks {
		result[market] = ob
	}

	return result
}

// GetMarkets returns list of available markets
func (m *OrderbookManager) GetMarkets() []string {
	m.orderbooksMu.RLock()
	defer m.orderbooksMu.RUnlock()

	markets := make([]string, 0, len(m.orderbooks))
	for market := range m.orderbooks {
		markets = append(markets, market)
	}

	return markets
}


