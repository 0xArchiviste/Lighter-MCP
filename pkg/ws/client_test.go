package ws

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestNewClientPath(t *testing.T) {
	cases := []struct {
		env, base, want string
	}{
		{"", "https://mainnet.zklighter.elliot.ai", "wss://mainnet.zklighter.elliot.ai/stream"},
		{"", "http://localhost:8080/", "ws://localhost:8080/stream"},
		{"/custom", "https://example.com", "wss://example.com/custom"},
	}
	for _, tc := range cases {
		t.Setenv("LIGHTER_WS_PATH", tc.env)
		c := NewClient(tc.base, "", nil)
		if c.wsURL != tc.want {
			t.Errorf("env=%q base=%q: got %q, want %q", tc.env, tc.base, c.wsURL, tc.want)
		}
		c.cancel()
	}
}

func TestFrameShapes(t *testing.T) {
	cases := []struct {
		f    Frame
		want string
	}{
		{SubscribeFrame(OrderBookChannel(0)), `{"type":"subscribe","channel":"order_book/0"}`},
		{UnsubscribeFrame(OrderBookChannel(42)), `{"type":"unsubscribe","channel":"order_book/42"}`},
		{Frame{Type: "pong"}, `{"type":"pong"}`},
	}
	for _, tc := range cases {
		b, err := json.Marshal(tc.f)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tc.want {
			t.Errorf("got %s, want %s", b, tc.want)
		}
	}
	if got := serverChannel("order_book/7"); got != "order_book:7" {
		t.Errorf("serverChannel = %q", got)
	}
}

// fakeStream mimics /stream with the frames seen from a live probe on
// 2026-10-25: a connected frame, subscribed/order_book with the full book,
// update/order_book deltas, "unsubscribed", and error frames with no channel.
type fakeStream struct {
	t        *testing.T
	mu       sync.Mutex
	received []string
	subs     map[string]bool
	pinged   bool
}

func (s *fakeStream) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/stream" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.WriteMessage(websocket.TextMessage, []byte(`{"session_id":"x","type":"connected"}`))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.received = append(s.received, strings.TrimSpace(string(raw)))
		s.mu.Unlock()

		var f map[string]any
		json.Unmarshal(raw, &f)
		typ, _ := f["type"].(string)
		ch, _ := f["channel"].(string)
		sch := strings.Replace(ch, "/", ":", 1)
		reply := func(s string) { conn.WriteMessage(websocket.TextMessage, []byte(s)) }
		switch {
		case typ == "pong":
		case typ != "subscribe" && typ != "unsubscribe":
			reply(`{"error":{"code":30001,"message":"Invalid Type"}}`)
		case !strings.HasPrefix(ch, "order_book/"):
			reply(`{"error":{"code":30005,"message":"Invalid Channel"}}`)
		case ch == "order_book/99999":
			reply(`{"error":{"code":30005,"message":"Invalid Channel:  (marketId)"}}`)
		case typ == "subscribe" && s.subs[sch]:
			reply(`{"error":{"code":30003,"message":"Already Subscribed to : ` + sch + `"}}`)
		case typ == "subscribe":
			s.subs[sch] = true
			reply(`{"channel":"` + sch + `","offset":1,"order_book":{"code":0,"asks":[{"price":"2367.09","size":"4.4559"},{"price":"2367.07","size":"0.0148"}],"bids":[{"price":"2366.90","size":"1.0"},{"price":"2367.05","size":"2.5"}],"offset":1},"type":"subscribed/order_book"}`)
			reply(`{"type":"ping"}`)
			reply(`{"channel":"` + sch + `","offset":2,"order_book":{"code":0,"asks":[{"price":"2367.07","size":"0.0000"}],"bids":[{"price":"2367.06","size":"3.0"}],"offset":2},"type":"update/order_book"}`)
		case typ == "unsubscribe" && !s.subs[sch]:
			reply(`{"error":{"code":30002,"message":"Not Subscribed to : ` + sch + `"}}`)
		default:
			delete(s.subs, sch)
			reply(`{"type": "unsubscribed", "channel": "` + sch + `"}`)
		}
	}
}

func dialFake(t *testing.T) (*Client, *fakeStream) {
	t.Helper()
	t.Setenv("LIGHTER_WS_PATH", "")
	fs := &fakeStream{t: t, subs: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(fs.handler))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "", nil)
	c.ackTimeout = 2 * time.Second
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c, fs
}

func TestSubscribeOrderBookAndUnsubscribe(t *testing.T) {
	c, fs := dialFake(t)
	m := NewOrderbookManager(c)

	if err := m.Subscribe(0, "ETH"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	var ob *OrderbookSnapshot
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := m.GetOrderbook("ETH"); err == nil && got.Offset == 2 {
			ob = got
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ob == nil {
		t.Fatal("update/order_book delta not applied")
	}
	if ob.Bids[0].Price != "2367.06" || ob.Asks[0].Price != "2367.09" {
		t.Errorf("best bid/ask = %s/%s, want 2367.06/2367.09", ob.Bids[0].Price, ob.Asks[0].Price)
	}
	if len(ob.Asks) != 1 || len(ob.Bids) != 3 {
		t.Errorf("levels asks=%d bids=%d, want 1 and 3", len(ob.Asks), len(ob.Bids))
	}

	// Duplicate subscribe surfaces the server error.
	err := c.Subscribe(OrderBookChannel(0), func(json.RawMessage) error { return nil })
	var serr *ServerError
	if !errors.As(err, &serr) || serr.Code != ErrCodeAlreadySubscribed {
		t.Errorf("duplicate subscribe err = %v", err)
	}

	if err := m.Unsubscribe(0); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if err := c.request(UnsubscribeFrame(OrderBookChannel(0)), "unsubscribed"); !errors.As(err, &serr) || serr.Code != ErrCodeNotSubscribed {
		t.Errorf("second unsubscribe err = %v", err)
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()
	want := []string{
		`{"type":"subscribe","channel":"order_book/0"}`,
		`{"type":"pong"}`,
		`{"type":"subscribe","channel":"order_book/0"}`,
		`{"type":"unsubscribe","channel":"order_book/0"}`,
		`{"type":"unsubscribe","channel":"order_book/0"}`,
	}
	if strings.Join(fs.received, "\n") != strings.Join(want, "\n") {
		t.Errorf("frames sent:\n%s\nwant:\n%s", strings.Join(fs.received, "\n"), strings.Join(want, "\n"))
	}
}

func TestSubscribeRejected(t *testing.T) {
	c, _ := dialFake(t)
	m := NewOrderbookManager(c)

	err := m.Subscribe(99999, "NOPE")
	var serr *ServerError
	if !errors.As(err, &serr) || serr.Code != ErrCodeInvalidChannel {
		t.Fatalf("err = %v, want code %d", err, ErrCodeInvalidChannel)
	}
	if _, err := m.GetOrderbookByID(99999); err == nil {
		t.Error("rejected market kept a book")
	}
	err = c.Subscribe("orderbook", func(json.RawMessage) error { return nil })
	if !errors.As(err, &serr) || serr.Code != ErrCodeInvalidChannel {
		t.Errorf("old channel err = %v", err)
	}
}

func TestConnectRejectedShowsBody(t *testing.T) {
	t.Setenv("LIGHTER_WS_PATH", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"code": 20558, "message": "You are accessing Lighter from a restricted jurisdiction."}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", nil)
	defer c.Close()
	err := c.Connect()
	if err == nil || !strings.Contains(err.Error(), "20558") || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("err = %v", err)
	}
}
