package ws

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"sync"
)

// PriceLevel is one order book level as Lighter sends it.
type PriceLevel struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

// OrderbookSnapshot is a market's book, bids best first (descending) and asks
// best first (ascending).
type OrderbookSnapshot struct {
	Market   string
	MarketID uint32
	Offset   int64
	Bids     []PriceLevel
	Asks     []PriceLevel
}

// orderBookFrame is a subscribed/order_book or update/order_book frame. The
// subscribed frame carries the full book; updates carry changed levels, with
// size zero meaning the level is gone.
type orderBookFrame struct {
	Type      string `json:"type"`
	Channel   string `json:"channel"`
	OrderBook struct {
		Asks   []PriceLevel `json:"asks"`
		Bids   []PriceLevel `json:"bids"`
		Offset int64        `json:"offset"`
	} `json:"order_book"`
}

type book struct {
	name   string
	id     uint32
	offset int64
	bids   map[string]string
	asks   map[string]string
}

// OrderbookManager keeps order books for subscribed markets.
type OrderbookManager struct {
	client  *Client
	books   map[uint32]*book
	booksMu sync.RWMutex
}

// NewOrderbookManager creates a new orderbook manager
func NewOrderbookManager(wsClient *Client) *OrderbookManager {
	return &OrderbookManager{
		client: wsClient,
		books:  make(map[uint32]*book),
	}
}

// Subscribe subscribes to order_book/<marketID>. name is the symbol used by
// GetOrderbook; it may be empty. A rejected subscribe is returned, not hidden.
func (m *OrderbookManager) Subscribe(marketID uint32, name string) error {
	m.booksMu.Lock()
	m.books[marketID] = &book{name: name, id: marketID, bids: map[string]string{}, asks: map[string]string{}}
	m.booksMu.Unlock()

	err := m.client.Subscribe(OrderBookChannel(marketID), func(data json.RawMessage) error {
		return m.handleOrderbookUpdate(marketID, data)
	})
	if err != nil {
		m.booksMu.Lock()
		delete(m.books, marketID)
		m.booksMu.Unlock()
	}
	return err
}

// Unsubscribe drops a market's book and unsubscribes from it.
func (m *OrderbookManager) Unsubscribe(marketID uint32) error {
	m.booksMu.Lock()
	delete(m.books, marketID)
	m.booksMu.Unlock()
	return m.client.Unsubscribe(OrderBookChannel(marketID))
}

// handleOrderbookUpdate applies a subscribed/order_book snapshot or an
// update/order_book delta.
func (m *OrderbookManager) handleOrderbookUpdate(marketID uint32, data json.RawMessage) error {
	var f orderBookFrame
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("parse order book frame: %w", err)
	}

	m.booksMu.Lock()
	defer m.booksMu.Unlock()
	b, ok := m.books[marketID]
	if !ok {
		return nil
	}

	switch f.Type {
	case "subscribed/order_book":
		b.bids = map[string]string{}
		b.asks = map[string]string{}
	case "update/order_book":
	default:
		return nil
	}
	applyLevels(b.bids, f.OrderBook.Bids)
	applyLevels(b.asks, f.OrderBook.Asks)
	b.offset = f.OrderBook.Offset
	return nil
}

func applyLevels(side map[string]string, levels []PriceLevel) {
	for _, l := range levels {
		if size, err := strconv.ParseFloat(l.Size, 64); err == nil && size == 0 {
			delete(side, l.Price)
			continue
		}
		side[l.Price] = l.Size
	}
}

func sortedLevels(side map[string]string, desc bool) []PriceLevel {
	out := make([]PriceLevel, 0, len(side))
	for p, s := range side {
		out = append(out, PriceLevel{Price: p, Size: s})
	}
	sort.Slice(out, func(i, j int) bool {
		pi, _ := strconv.ParseFloat(out[i].Price, 64)
		pj, _ := strconv.ParseFloat(out[j].Price, 64)
		if desc {
			return pi > pj
		}
		return pi < pj
	})
	return out
}

func (b *book) snapshot() *OrderbookSnapshot {
	return &OrderbookSnapshot{
		Market:   b.name,
		MarketID: b.id,
		Offset:   b.offset,
		Bids:     sortedLevels(b.bids, true),
		Asks:     sortedLevels(b.asks, false),
	}
}

// GetOrderbook returns the book for a market symbol. It fails until the
// subscribe snapshot has arrived.
func (m *OrderbookManager) GetOrderbook(market string) (*OrderbookSnapshot, error) {
	m.booksMu.RLock()
	defer m.booksMu.RUnlock()

	for _, b := range m.books {
		if b.name == market && (len(b.bids) > 0 || len(b.asks) > 0) {
			return b.snapshot(), nil
		}
	}
	return nil, fmt.Errorf("orderbook not found for market: %s", market)
}

// GetOrderbookByID returns the book for a market id.
func (m *OrderbookManager) GetOrderbookByID(marketID uint32) (*OrderbookSnapshot, error) {
	m.booksMu.RLock()
	defer m.booksMu.RUnlock()

	b, ok := m.books[marketID]
	if !ok || (len(b.bids) == 0 && len(b.asks) == 0) {
		return nil, fmt.Errorf("orderbook not found for market id: %d", marketID)
	}
	return b.snapshot(), nil
}

// GetAllOrderbooks returns every book that has data, keyed by symbol (or
// market id when no symbol was given).
func (m *OrderbookManager) GetAllOrderbooks() map[string]*OrderbookSnapshot {
	m.booksMu.RLock()
	defer m.booksMu.RUnlock()

	result := make(map[string]*OrderbookSnapshot)
	for _, b := range m.books {
		if len(b.bids) == 0 && len(b.asks) == 0 {
			continue
		}
		key := b.name
		if key == "" {
			key = strconv.FormatUint(uint64(b.id), 10)
		}
		result[key] = b.snapshot()
	}
	return result
}

// GetMarkets returns the symbols of books that have data.
func (m *OrderbookManager) GetMarkets() []string {
	all := m.GetAllOrderbooks()
	markets := make([]string, 0, len(all))
	for market := range all {
		markets = append(markets, market)
	}
	return markets
}
