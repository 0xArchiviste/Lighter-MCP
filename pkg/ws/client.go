package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Client manages WebSocket connections to Lighter API
type Client struct {
	baseURL      string
	wsURL        string
	authToken    string
	dialer       *websocket.Dialer
	conn         *websocket.Conn
	connMu       sync.RWMutex
	handlers     map[string]MessageHandler
	handlersMu   sync.RWMutex
	reconnect    bool
	ctx          context.Context
	cancel       context.CancelFunc
	subscribed   map[string]bool
	subscribedMu sync.RWMutex
}

// MessageHandler handles incoming WebSocket messages
type MessageHandler func(data json.RawMessage) error

// WSMessage represents a WebSocket message
type WSMessage struct {
	Channel string          `json:"channel,omitempty"`
	Type    string          `json:"type,omitempty"`
	Market  string          `json:"market,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
}

// SubscriptionMessage represents a subscription request
type SubscriptionMessage struct {
	Method string      `json:"method"`
	Params interface{} `json:"params,omitempty"`
	Channel string     `json:"channel,omitempty"`
	Topic   string     `json:"topic,omitempty"`
}

// OrderbookSnapshot represents orderbook data from WebSocket
type OrderbookSnapshot struct {
	Market string      `json:"market"`
	Bids   [][]string  `json:"bids"` // [price, size]
	Asks   [][]string  `json:"asks"` // [price, size]
}

// TradeUpdate represents a trade from WebSocket
type TradeUpdate struct {
	Market    string `json:"market"`
	Price     string `json:"price"`
	Size      string `json:"size"`
	Side      string `json:"side"`
	Timestamp int64  `json:"timestamp"`
}

// NewClient creates a new WebSocket client
func NewClient(baseURL string, authToken string) *Client {
	// Convert HTTP(S) URL to WebSocket URL
	wsURL := baseURL
	if len(wsURL) >= 5 && wsURL[:5] == "https" {
		wsURL = "wss" + wsURL[5:]
	} else if len(wsURL) >= 4 && wsURL[:4] == "http" {
		wsURL = "ws" + wsURL[4:]
	}
	
	// Default WebSocket endpoint
	if wsURL[len(wsURL)-1] != '/' {
		wsURL += "/"
	}
	wsURL += "ws"

	ctx, cancel := context.WithCancel(context.Background())

	return &Client{
		baseURL:    baseURL,
		wsURL:      wsURL,
		authToken:  authToken,
		dialer: &websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
		},
		handlers:   make(map[string]MessageHandler),
		reconnect:  true,
		ctx:        ctx,
		cancel:     cancel,
		subscribed: make(map[string]bool),
	}
}

// Connect establishes WebSocket connection
func (c *Client) Connect() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	headers := http.Header{}
	if c.authToken != "" {
		// Check if auth token is in SDK format (contains colons)
		if len(c.authToken) > 0 && c.authToken[0] != 'B' {
			headers.Set("Authorization", c.authToken)
		} else {
			headers.Set("Authorization", "Bearer "+c.authToken)
		}
	}

	conn, _, err := c.dialer.Dial(c.wsURL, headers)
	if err != nil {
		return fmt.Errorf("failed to connect to websocket: %w", err)
	}

	c.conn = conn
	log.Printf("WebSocket connected to %s", c.wsURL)

	// Start message reader
	go c.readMessages()

	return nil
}

// readMessages reads messages from WebSocket connection
func (c *Client) readMessages() {
	defer func() {
		c.connMu.Lock()
		if c.conn != nil {
			c.conn.Close()
			c.conn = nil
		}
		c.connMu.Unlock()

		if c.reconnect {
			log.Println("WebSocket disconnected, reconnecting in 5 seconds...")
			time.Sleep(5 * time.Second)
			if err := c.Connect(); err != nil {
				log.Printf("Failed to reconnect: %v", err)
			}
		}
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.connMu.RLock()
			conn := c.conn
			c.connMu.RUnlock()

			if conn == nil {
				return
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Error reading WebSocket message: %v", err)
				return
			}

			c.handleMessage(message)
		}
	}
}

// handleMessage processes incoming WebSocket messages
func (c *Client) handleMessage(message []byte) {
	var msg WSMessage
	if err := json.Unmarshal(message, &msg); err != nil {
		// Try parsing as direct data
		c.handlersMu.RLock()
		defer c.handlersMu.RUnlock()

		// Try to find a handler for "all" or default
		if handler, exists := c.handlers["all"]; exists {
			if err := handler(message); err != nil {
				log.Printf("Error handling message: %v", err)
			}
		}
		return
	}

	// Determine channel/key for handler lookup
	channel := msg.Channel
	if channel == "" {
		channel = msg.Type
	}
	if channel == "" && msg.Market != "" {
		channel = msg.Market
	}

	c.handlersMu.RLock()
	handler, exists := c.handlers[channel]
	if !exists {
		handler, exists = c.handlers["all"]
	}
	c.handlersMu.RUnlock()

	if exists {
		data := msg.Data
		if len(data) == 0 {
			data = msg.Message
		}
		if len(data) == 0 {
			data = message
		}
		if err := handler(data); err != nil {
			log.Printf("Error handling message for channel %s: %v", channel, err)
		}
	}
}

// Subscribe subscribes to a WebSocket channel
func (c *Client) Subscribe(channel string, subscription interface{}, handler MessageHandler) error {
	c.handlersMu.Lock()
	c.handlers[channel] = handler
	c.handlersMu.Unlock()

	c.subscribedMu.Lock()
	c.subscribed[channel] = true
	c.subscribedMu.Unlock()

	// Try different subscription message formats
	subMsgs := []interface{}{
		SubscriptionMessage{
			Method: "subscribe",
			Channel: channel,
			Params: subscription,
		},
		SubscriptionMessage{
			Method: "subscribe",
			Topic: channel,
			Params: subscription,
		},
		map[string]interface{}{
			"method": "subscribe",
			"channel": channel,
			"params": subscription,
		},
		map[string]interface{}{
			"method": "subscribe",
			"topic": channel,
			"params": subscription,
		},
		subscription, // Try sending subscription directly
	}

	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()

	if conn == nil {
		return fmt.Errorf("websocket not connected")
	}

	var lastErr error
	for _, subMsg := range subMsgs {
		if err := conn.WriteJSON(subMsg); err == nil {
			log.Printf("Subscribed to channel: %s", channel)
			return nil
		} else {
			lastErr = err
		}
	}

	return fmt.Errorf("failed to subscribe to %s: %w", channel, lastErr)
}

// Unsubscribe unsubscribes from a channel
func (c *Client) Unsubscribe(channel string) error {
	c.handlersMu.Lock()
	delete(c.handlers, channel)
	c.handlersMu.Unlock()

	c.subscribedMu.Lock()
	delete(c.subscribed, channel)
	c.subscribedMu.Unlock()

	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()

	if conn == nil {
		return nil
	}

	unsubMsg := map[string]interface{}{
		"method": "unsubscribe",
		"channel": channel,
	}

	return conn.WriteJSON(unsubMsg)
}

// Close closes the WebSocket connection
func (c *Client) Close() error {
	c.reconnect = false
	c.cancel()

	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// IsConnected returns whether the WebSocket is connected
func (c *Client) IsConnected() bool {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.conn != nil
}

// SetAuthToken updates the auth token
func (c *Client) SetAuthToken(token string) {
	c.authToken = token
}


