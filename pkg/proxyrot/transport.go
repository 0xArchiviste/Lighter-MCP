package proxyrot

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxProxyAttempts = 3

// Client returns an HTTP client that sends each request through the next proxy.
// Connection failures try the following proxies, up to maxProxyAttempts.
func Client(pool *Pool, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &rotatingTransport{pool: pool},
	}
}

type rotatingTransport struct {
	pool *Pool
	mu   sync.Mutex
	by   map[string]*http.Transport
}

func (t *rotatingTransport) transport(e Entry) *http.Transport {
	key := e.String() + "\n" + e.User
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.by == nil {
		t.by = make(map[string]*http.Transport)
	}
	if tr, ok := t.by[key]; ok {
		return tr
	}
	base, _ := http.DefaultTransport.(*http.Transport)
	tr := base.Clone()
	tr.Proxy = http.ProxyURL(e.URL())
	tr.TLSHandshakeTimeout = 15 * time.Second
	t.by[key] = tr
	return tr
}

func (t *rotatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	attempts := t.pool.Len()
	if attempts > maxProxyAttempts {
		attempts = maxProxyAttempts
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 && req.Body != nil {
			if req.GetBody == nil {
				return nil, lastErr
			}
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = body
		}
		entry := t.pool.Next()
		resp, err := t.transport(entry).RoundTrip(req)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", entry.HostPort(), err)
			continue
		}
		if shouldRotate(resp) {
			lastErr = fmt.Errorf("%s: HTTP %d", entry.HostPort(), resp.StatusCode)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no proxy available")
	}
	return nil, lastErr
}

// shouldRotate reports responses that mean this exit was refused before the API
// handled the call, so the next proxy in the file should try the same request.
func shouldRotate(resp *http.Response) bool {
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return true
	case http.StatusForbidden:
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		// JSON is an application response. Anything else (HTML challenge, empty
		// block page) is the edge in front of the endpoint.
		return !strings.Contains(ct, "json")
	default:
		return false
	}
}
