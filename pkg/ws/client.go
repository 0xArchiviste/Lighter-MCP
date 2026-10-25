package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Client manages a WebSocket connection to Lighter's /stream endpoint.
type Client struct {
	baseURL      string
	wsURL        string
	authToken    string
	dialer       *websocket.Dialer
	conn         *websocket.Conn
	connMu       sync.RWMutex
	writeMu      sync.Mutex
	handlers     map[string]MessageHandler
	handlersMu   sync.RWMutex
	reconnect    bool
	ctx          context.Context
	cancel       context.CancelFunc
	subscribed   map[string]bool
	subscribedMu sync.RWMutex

	// reqMu serializes subscribe/unsubscribe so a server error, which carries
	// no channel, can be attributed to the one request in flight.
	reqMu      sync.Mutex
	pending    *pendingRequest
	pendingMu  sync.Mutex
	ackTimeout time.Duration
}

type pendingRequest struct {
	channel string // server form, e.g. "order_book:0"
	ackType string // "subscribed/" prefix or "unsubscribed"
	done    chan error
}

// MessageHandler receives the full JSON frame for a subscribed channel.
type MessageHandler func(data json.RawMessage) error

// Frame is a client-to-server control frame on /stream.
type Frame struct {
	Type    string `json:"type"`
	Channel string `json:"channel,omitempty"`
}

// ServerError is an error frame from /stream, e.g.
// {"error":{"code":30005,"message":"Invalid Channel"}}.
type ServerError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("lighter ws error %d: %s", e.Code, e.Message)
}

// Error codes observed on /stream.
const (
	ErrCodeInvalidType       = 30001
	ErrCodeNotSubscribed     = 30002
	ErrCodeAlreadySubscribed = 30003
	ErrCodeInvalidChannel    = 30005
)

// inbound is the envelope of a server-to-client frame.
type inbound struct {
	Type    string       `json:"type"`
	Channel string       `json:"channel"`
	Error   *ServerError `json:"error"`
}

// DefaultPath is Lighter's WebSocket endpoint. The old "ws" path returns 403.
const DefaultPath = "stream"

// Path returns LIGHTER_WS_PATH when set, else DefaultPath.
func Path() string {
	if p := strings.TrimSpace(os.Getenv("LIGHTER_WS_PATH")); p != "" {
		return p
	}
	return DefaultPath
}

// OrderBookChannel is the subscribe channel for a market's order book.
func OrderBookChannel(marketID uint32) string {
	return fmt.Sprintf("order_book/%d", marketID)
}

// SubscribeFrame builds {"type":"subscribe","channel":channel}.
func SubscribeFrame(channel string) Frame {
	return Frame{Type: "subscribe", Channel: channel}
}

// UnsubscribeFrame builds {"type":"unsubscribe","channel":channel}.
func UnsubscribeFrame(channel string) Frame {
	return Frame{Type: "unsubscribe", Channel: channel}
}

// serverChannel maps a subscribe channel ("order_book/0") to the form the
// server echoes in acks and updates ("order_book:0").
func serverChannel(channel string) string {
	return strings.Replace(channel, "/", ":", 1)
}

// NewClient creates a new WebSocket client. proxy is optional; when set, the
// dial stays on that proxy for the life of the connection.
func NewClient(baseURL string, authToken string, proxy *url.URL) *Client {
	// Convert HTTP(S) URL to WebSocket URL
	wsURL := baseURL
	if len(wsURL) >= 5 && wsURL[:5] == "https" {
		wsURL = "wss" + wsURL[5:]
	} else if len(wsURL) >= 4 && wsURL[:4] == "http" {
		wsURL = "ws" + wsURL[4:]
	}

	if wsURL[len(wsURL)-1] != '/' {
		wsURL += "/"
	}
	wsURL += strings.TrimPrefix(Path(), "/")

	ctx, cancel := context.WithCancel(context.Background())

	handshake := 10 * time.Second
	if proxy != nil {
		handshake = 20 * time.Second
	}

	return &Client{
		baseURL:   baseURL,
		wsURL:     wsURL,
		authToken: authToken,
		dialer: &websocket.Dialer{
			HandshakeTimeout: handshake,
			Proxy: func(*http.Request) (*url.URL, error) {
				return proxy, nil
			},
		},
		handlers:   make(map[string]MessageHandler),
		reconnect:  true,
		ctx:        ctx,
		cancel:     cancel,
		subscribed: make(map[string]bool),
		ackTimeout: 5 * time.Second,
	}
}

// Connect dials /stream and waits for the server's {"type":"connected"} frame.
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

	conn, resp, err := c.dialer.Dial(c.wsURL, headers)
	if err != nil {
		// A rejected handshake carries Lighter's reason in the body, e.g.
		// 400 {"code":20558,...} for a restricted jurisdiction.
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			return fmt.Errorf("failed to connect to websocket: %w (HTTP %d: %s)", err, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("failed to connect to websocket: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(c.dialer.HandshakeTimeout))
	var hello inbound
	if err := conn.ReadJSON(&hello); err != nil {
		conn.Close()
		return fmt.Errorf("websocket: waiting for connected frame: %w", err)
	}
	conn.SetReadDeadline(time.Time{})
	if hello.Error != nil {
		conn.Close()
		return fmt.Errorf("websocket: %w", hello.Error)
	}
	if hello.Type != "connected" {
		conn.Close()
		return fmt.Errorf("websocket: expected connected frame, got type %q", hello.Type)
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
		c.failPending(errors.New("websocket disconnected"))

		if c.reconnect {
			log.Println("WebSocket disconnected, reconnecting in 5 seconds...")
			time.Sleep(5 * time.Second)
			if err := c.Connect(); err != nil {
				log.Printf("Failed to reconnect: %v", err)
				return
			}
			c.resubscribe()
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
				if c.ctx.Err() == nil {
					log.Printf("Error reading WebSocket message: %v", err)
				}
				return
			}

			c.handleMessage(message)
		}
	}
}

// handleMessage processes incoming WebSocket messages
func (c *Client) handleMessage(message []byte) {
	var msg inbound
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Printf("WebSocket: unparseable frame: %v", err)
		return
	}

	switch {
	case msg.Error != nil:
		if !c.resolvePending(msg.Error, "", "") {
			log.Printf("WebSocket: %v", msg.Error)
		}
		return
	case msg.Type == "ping":
		if err := c.writeFrame(Frame{Type: "pong"}); err != nil {
			log.Printf("WebSocket: pong failed: %v", err)
		}
		return
	case msg.Type == "unsubscribed":
		c.resolvePending(nil, msg.Type, msg.Channel)
		return
	case strings.HasPrefix(msg.Type, "subscribed/"):
		c.resolvePending(nil, msg.Type, msg.Channel)
	}

	c.handlersMu.RLock()
	handler, exists := c.handlers[msg.Channel]
	c.handlersMu.RUnlock()

	if exists {
		if err := handler(message); err != nil {
			log.Printf("Error handling message for channel %s: %v", msg.Channel, err)
		}
	}
}

// resolvePending completes the in-flight request when the frame answers it.
// An error frame answers whatever is in flight.
func (c *Client) resolvePending(serr *ServerError, typ, channel string) bool {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	p := c.pending
	if p == nil {
		return false
	}
	if serr == nil {
		if channel != p.channel {
			return false
		}
		if p.ackType == "unsubscribed" && typ != "unsubscribed" {
			return false
		}
		if p.ackType != "unsubscribed" && !strings.HasPrefix(typ, p.ackType) {
			return false
		}
		p.done <- nil
	} else {
		p.done <- serr
	}
	c.pending = nil
	return true
}

func (c *Client) failPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if c.pending != nil {
		c.pending.done <- err
		c.pending = nil
	}
}

func (c *Client) writeFrame(f Frame) error {
	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()
	if conn == nil {
		return fmt.Errorf("websocket not connected")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return conn.WriteJSON(f)
}

// request sends f and waits for its ack or an error frame.
func (c *Client) request(f Frame, ackType string) error {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	p := &pendingRequest{channel: serverChannel(f.Channel), ackType: ackType, done: make(chan error, 1)}
	c.pendingMu.Lock()
	c.pending = p
	c.pendingMu.Unlock()

	if err := c.writeFrame(f); err != nil {
		c.pendingMu.Lock()
		if c.pending == p {
			c.pending = nil
		}
		c.pendingMu.Unlock()
		return err
	}

	timer := time.NewTimer(c.ackTimeout)
	defer timer.Stop()
	select {
	case err := <-p.done:
		return err
	case <-timer.C:
		c.pendingMu.Lock()
		if c.pending == p {
			c.pending = nil
		}
		c.pendingMu.Unlock()
		return fmt.Errorf("no %s ack for %s within %s", f.Type, f.Channel, c.ackTimeout)
	}
}

// Subscribe sends {"type":"subscribe","channel":channel} (for example
// OrderBookChannel(0)) and waits for the server's subscribed/* ack. handler
// receives every frame for the channel, including the initial snapshot. A
// rejected subscribe returns the server's *ServerError.
func (c *Client) Subscribe(channel string, handler MessageHandler) error {
	key := serverChannel(channel)
	c.handlersMu.Lock()
	c.handlers[key] = handler
	c.handlersMu.Unlock()

	if err := c.request(SubscribeFrame(channel), "subscribed/"); err != nil {
		var serr *ServerError
		if !(errors.As(err, &serr) && serr.Code == ErrCodeAlreadySubscribed) {
			c.handlersMu.Lock()
			delete(c.handlers, key)
			c.handlersMu.Unlock()
		}
		return fmt.Errorf("subscribe %s: %w", channel, err)
	}

	c.subscribedMu.Lock()
	c.subscribed[channel] = true
	c.subscribedMu.Unlock()
	log.Printf("Subscribed to channel: %s", channel)
	return nil
}

// Unsubscribe sends {"type":"unsubscribe","channel":channel} and waits for
// the server's "unsubscribed" ack.
func (c *Client) Unsubscribe(channel string) error {
	c.handlersMu.Lock()
	delete(c.handlers, serverChannel(channel))
	c.handlersMu.Unlock()

	c.subscribedMu.Lock()
	delete(c.subscribed, channel)
	c.subscribedMu.Unlock()

	if !c.IsConnected() {
		return nil
	}
	if err := c.request(UnsubscribeFrame(channel), "unsubscribed"); err != nil {
		return fmt.Errorf("unsubscribe %s: %w", channel, err)
	}
	return nil
}

// resubscribe restores subscriptions after a reconnect.
func (c *Client) resubscribe() {
	c.subscribedMu.RLock()
	channels := make([]string, 0, len(c.subscribed))
	for ch := range c.subscribed {
		channels = append(channels, ch)
	}
	c.subscribedMu.RUnlock()

	for _, ch := range channels {
		if err := c.request(SubscribeFrame(ch), "subscribed/"); err != nil {
			log.Printf("WebSocket: resubscribe %s failed: %v", ch, err)
		}
	}
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
