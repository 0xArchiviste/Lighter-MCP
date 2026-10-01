package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/proxyrot"
	lighterclient "github.com/elliottech/lighter-go/client"
)

type Client struct {
	cfg     config.Config
	client  *http.Client
	baseURL string
	proxies *proxyrot.Pool
	// SDK HTTP client. Uses the same transport as client, including proxy rotation.
	sdkClient lighterclient.MinimalHTTPClient
}

func New(cfg config.Config) *Client {
	hc := &http.Client{Timeout: 15 * time.Second}
	var pool *proxyrot.Pool
	if p, path, err := loadProxies(cfg.ProxyFile); err != nil {
		fmt.Fprintf(os.Stderr, "[proxy] %v\n", err)
	} else if p != nil {
		pool = p
		hc = proxyrot.Client(p, 30*time.Second)
		fmt.Fprintf(os.Stderr, "[proxy] rotating %d proxies from %s\n", p.Len(), path)
	}

	var sdkClient lighterclient.MinimalHTTPClient
	if cfg.BaseURL != "" {
		sdkClient = &sdkHTTP{endpoint: cfg.BaseURL, http: hc}
	}

	return &Client{
		cfg:       cfg,
		client:    hc,
		baseURL:   cfg.BaseURL,
		proxies:   pool,
		sdkClient: sdkClient,
	}
}

// loadProxies opens cfg path, or proxies.txt when unset. A missing default file
// means direct connections. An explicit path that cannot be read is an error.
func loadProxies(path string) (*proxyrot.Pool, string, error) {
	if path == "" {
		path = "proxies.txt"
	}
	p, err := proxyrot.LoadFile(path)
	if err != nil {
		if os.IsNotExist(err) && (path == "proxies.txt") {
			return nil, path, nil
		}
		return nil, path, err
	}
	return p, path, nil
}

// NextProxy returns the next proxy URL for a new connection, or nil for a direct dial.
func (c *Client) NextProxy() *url.URL {
	if c.proxies == nil {
		return nil
	}
	return c.proxies.NextURL()
}

// GetSDKClient returns the SDK HTTP client
func (c *Client) GetSDKClient() lighterclient.MinimalHTTPClient {
	return c.sdkClient
}

// BaseURL returns the base URL of the API client
func (c *Client) BaseURL() string {
	return c.baseURL
}

func (c *Client) setAuthHeader(req *http.Request, authToken string) {
	if authToken != "" {
		// Official SDK format: "deadline:account_index:api_key_index:signature"
		// Check if it's already in SDK format (contains colons) or legacy format
		if strings.Contains(authToken, ":") {
			// SDK format - use as-is without Bearer prefix
			req.Header.Set("Authorization", authToken)
			// Debug: log Authorization header format (first 50 chars only for security)
			if strings.Contains(req.URL.Path, "order") || strings.Contains(req.URL.Path, "Order") {
				fmt.Fprintf(os.Stderr, "[DEBUG] Setting Authorization header for orders endpoint (format: SDK, length: %d)\n", len(authToken))
			}
		} else {
			// Legacy format - use Bearer prefix
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))
			if strings.Contains(req.URL.Path, "order") || strings.Contains(req.URL.Path, "Order") {
				fmt.Fprintf(os.Stderr, "[DEBUG] Setting Authorization header for orders endpoint (format: Bearer, length: %d)\n", len(authToken))
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "[WARNING] No auth token provided for request to %s\n", req.URL.Path)
	}

	// Set headers to match the curl example exactly (for nextNonce endpoint)
	// These headers help ensure the request matches the frontend format
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:144.0) Gecko/20100101 Firefox/144.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", "https://app.lighter.xyz/")
	req.Header.Set("Origin", "https://app.lighter.xyz")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Cache-Control", "no-cache")
}

func (c *Client) doRequest(ctx context.Context, method, endpoint string, body io.Reader, authToken string) (*http.Response, error) {
	// Handle URL construction properly for both formats
	// api/v1/... (no leading slash) and /v1/... (leading slash)
	var fullURL string
	if strings.HasPrefix(endpoint, "api/") {
		// Official SDK format - ensure baseURL ends with / and endpoint doesn't start with /
		baseURL := strings.TrimSuffix(c.baseURL, "/")
		fullURL = fmt.Sprintf("%s/%s", baseURL, endpoint)
	} else {
		// Legacy format - ensure proper joining
		baseURL := strings.TrimSuffix(c.baseURL, "/")
		fullURL = fmt.Sprintf("%s%s", baseURL, endpoint)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set Content-Type only for POST/PUT requests with body (curl example doesn't set it for GET)
	if body != nil && (method == "POST" || method == "PUT") {
		req.Header.Set("Content-Type", "application/json")
	}

	// Always set auth header and browser-like headers (matching curl example exactly)
	if authToken != "" {
		c.setAuthHeader(req, authToken)
	} else {
		// Even without auth token, set browser-like headers for GET requests (matching curl)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:144.0) Gecko/20100101 Firefox/144.0")
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")
		req.Header.Set("Referer", "https://app.lighter.xyz/")
		req.Header.Set("Origin", "https://app.lighter.xyz")
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Pragma", "no-cache")
		req.Header.Set("Cache-Control", "no-cache")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

func (c *Client) parseResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}

	return nil
}

// CreateAuthTokenWithExpiry creates an auth token using the signer
// This should be called via the signer client, not directly
func (c *Client) CreateAuthTokenWithExpiry(ctx context.Context, expirySeconds int64) (string, error) {
	// Placeholder: token creation is signed via signer; integrate when signer implemented.
	return "", fmt.Errorf("not implemented - use signer.CreateAuthTokenWithExpiry")
}

// NextNonce gets the next nonce for the API key
// Uses official SDK endpoint format: api/v1/nextNonce
func (c *Client) NextNonce(ctx context.Context) (uint64, error) {
	// Try official SDK client first if available
	if c.sdkClient != nil {
		nonce, err := c.sdkClient.GetNextNonce(int64(c.cfg.AccountIndex), uint8(c.cfg.APIKeyIndex))
		if err == nil {
			return uint64(nonce), nil
		}
		// Fall through to REST endpoint if SDK fails
	}

	// Try multiple REST endpoint formats
	endpoints := []string{
		fmt.Sprintf("api/v1/nextNonce?account_index=%d&api_key_index=%d", c.cfg.AccountIndex, c.cfg.APIKeyIndex),  // Official SDK format (camelCase)
		fmt.Sprintf("api/v1/next_nonce?account_index=%d&api_key_index=%d", c.cfg.AccountIndex, c.cfg.APIKeyIndex), // Snake case format
		fmt.Sprintf("/v1/next_nonce?account_index=%d&api_key_index=%d", c.cfg.AccountIndex, c.cfg.APIKeyIndex),    // Legacy format
	}

	var lastErr error
	for _, endpoint := range endpoints {
		resp, err := c.doRequest(ctx, "GET", endpoint, nil, "")
		if err == nil {
			var result struct {
				Nonce uint64 `json:"nonce"`
			}
			if err := c.parseResponse(resp, &result); err == nil {
				return result.Nonce, nil
			}
			lastErr = err
		} else {
			lastErr = err
		}
	}

	if lastErr != nil {
		return 0, fmt.Errorf("failed to get nonce from any endpoint: %w", lastErr)
	}
	return 0, fmt.Errorf("failed to get nonce: all endpoints returned errors")
}

// NextNonceWithAuth gets the next nonce for the API key with authentication
func (c *Client) NextNonceWithAuth(ctx context.Context, authToken string) (uint64, error) {
	// Skip SDK client - it might return incorrect nonces
	// Always use REST API with auth token for reliable nonce retrieval
	// Match the exact curl format: /api/v1/nextNonce?account_index=X&api_key_index=Y

	// Use the exact endpoint format from the curl example
	endpoints := []string{
		fmt.Sprintf("api/v1/nextNonce?account_index=%d&api_key_index=%d", c.cfg.AccountIndex, c.cfg.APIKeyIndex),  // Exact curl format
		fmt.Sprintf("/api/v1/nextNonce?account_index=%d&api_key_index=%d", c.cfg.AccountIndex, c.cfg.APIKeyIndex), // With leading slash
	}

	var errors []string
	for _, endpoint := range endpoints {
		resp, err := c.doRequest(ctx, "GET", endpoint, nil, authToken)
		if err == nil {
			// Read response body to check structure
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()

			if readErr != nil {
				errors = append(errors, fmt.Sprintf("%s: failed to read response: %v", endpoint, readErr))
				continue
			}

			// Try to parse as expected structure with code field: {"code":200,"nonce":88}
			var resultWithCode struct {
				Code  int    `json:"code"`
				Nonce uint64 `json:"nonce"`
			}
			if err := json.Unmarshal(body, &resultWithCode); err == nil && resultWithCode.Nonce > 0 {
				fmt.Fprintf(os.Stderr, "[DEBUG] Parsed nonce response with code: code=%d, nonce=%d\n", resultWithCode.Code, resultWithCode.Nonce)
				return resultWithCode.Nonce, nil
			}

			// Try to parse as expected structure without code field
			var result struct {
				Nonce uint64 `json:"nonce"`
			}
			if err := json.Unmarshal(body, &result); err == nil && result.Nonce > 0 {
				fmt.Fprintf(os.Stderr, "[DEBUG] Parsed nonce response: nonce=%d\n", result.Nonce)
				return result.Nonce, nil
			}

			// Try alternative structure (wrapped response)
			var wrappedResult struct {
				Data struct {
					Nonce uint64 `json:"nonce"`
				} `json:"data"`
			}
			if err := json.Unmarshal(body, &wrappedResult); err == nil && wrappedResult.Data.Nonce > 0 {
				fmt.Fprintf(os.Stderr, "[DEBUG] Parsed nonce response wrapped: nonce=%d\n", wrappedResult.Data.Nonce)
				return wrappedResult.Data.Nonce, nil
			}

			// Try direct number
			var directNonce uint64
			if err := json.Unmarshal(body, &directNonce); err == nil && directNonce > 0 {
				fmt.Fprintf(os.Stderr, "[DEBUG] Parsed nonce as direct number: nonce=%d\n", directNonce)
				return directNonce, nil
			}

			errors = append(errors, fmt.Sprintf("%s: unexpected response format: %s", endpoint, string(body)))
		} else {
			errors = append(errors, fmt.Sprintf("%s: %v", endpoint, err))
		}
	}

	return 0, fmt.Errorf("failed to get nonce from any endpoint. Errors: %s", strings.Join(errors, "; "))
}

func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// toSnakeCase converts CamelCase to snake_case
func toSnakeCase(s string) string {
	var result []rune
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result = append(result, '_')
		}
		if r >= 'A' && r <= 'Z' {
			result = append(result, r+'a'-'A')
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

type SignedTx struct {
	Payload   interface{} `json:"payload"` // Transaction payload object
	Signature string      `json:"signature"`
	TxType    interface{} `json:"tx_type"` // Transaction type (uint8) - required at top level
}

// SendTx sends a single signed transaction
func (c *Client) SendTx(ctx context.Context, tx SignedTx) (string, error) {
	// If payload is a map, try flattening it (merge payload fields into top level)
	var body []byte
	var err error

	if payloadMap, ok := tx.Payload.(map[string]interface{}); ok {
		// Flatten: merge payload fields into top level and convert to snake_case
		flatTx := make(map[string]interface{})
		for k, v := range payloadMap {
			// Convert CamelCase to snake_case
			snakeKey := toSnakeCase(k)
			flatTx[snakeKey] = v
		}
		// Add signature and tx_type at top level (they might already be in payload)
		flatTx["signature"] = tx.Signature
		flatTx["tx_type"] = tx.TxType
		body, err = json.Marshal(flatTx)
	} else {
		// Use original structure
		body, err = json.Marshal(tx)
	}

	if err != nil {
		return "", fmt.Errorf("failed to marshal tx: %w", err)
	}

	// Debug: log the actual payload being sent
	fmt.Fprintf(os.Stderr, "[DEBUG] Transaction payload: %s\n", string(body))

	// Try multiple endpoint formats (official SDK format first)
	endpoints := []string{
		"api/v1/sendTx",   // Official SDK format (camelCase)
		"api/v1/send_tx",  // Snake case format
		"/api/v1/sendTx",  // Alternative format
		"/api/v1/send_tx", // Alternative snake case
		"/v1/send_tx",     // Legacy format
	}

	var errors []string
	for _, endpoint := range endpoints {
		// Try with empty auth first, then we'll add SendTxWithAuth if needed
		resp, err := c.doRequest(ctx, "POST", endpoint, bytes.NewBuffer(body), "")
		if err == nil {
			// Read response body to check structure
			bodyBytes, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()

			if readErr != nil {
				errors = append(errors, fmt.Sprintf("%s: failed to read response: %v", endpoint, readErr))
				continue
			}

			// Check status code
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				errors = append(errors, fmt.Sprintf("%s: HTTP %d - %s", endpoint, resp.StatusCode, string(bodyBytes)))
				continue
			}

			// Try to parse as expected structure
			var result struct {
				TxHash string `json:"tx_hash"`
			}
			if err := json.Unmarshal(bodyBytes, &result); err == nil && result.TxHash != "" {
				return result.TxHash, nil
			}

			// Try alternative structure (wrapped response)
			var wrappedResult struct {
				Data struct {
					TxHash string `json:"tx_hash"`
				} `json:"data"`
			}
			if err := json.Unmarshal(bodyBytes, &wrappedResult); err == nil && wrappedResult.Data.TxHash != "" {
				return wrappedResult.Data.TxHash, nil
			}

			// Try direct string
			var directHash string
			if err := json.Unmarshal(bodyBytes, &directHash); err == nil && directHash != "" {
				return directHash, nil
			}

			errors = append(errors, fmt.Sprintf("%s: unexpected response format: %s", endpoint, string(bodyBytes)))
		} else {
			errors = append(errors, fmt.Sprintf("%s: %v", endpoint, err))
		}
	}

	return "", fmt.Errorf("failed to send transaction via any endpoint. Errors: %s", strings.Join(errors, "; "))
}

// SendTxBatch sends multiple signed transactions
func (c *Client) SendTxBatch(ctx context.Context, txs []SignedTx) ([]string, error) {
	body, err := json.Marshal(txs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tx batch: %w", err)
	}

	resp, err := c.doRequest(ctx, "POST", "/v1/send_tx_batch", bytes.NewBuffer(body), "")
	if err != nil {
		return nil, err
	}

	var result struct {
		TxHashes []string `json:"tx_hashes"`
	}
	if err := c.parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return result.TxHashes, nil
}

// OrderBookDetails gets orderbook data for a specific market
// Based on Python SDK: GET /api/v1/orderBookDetails
func (c *Client) OrderBookDetails(ctx context.Context, market string, authToken string) (json.RawMessage, error) {
	// Try multiple endpoint formats - Python SDK uses camelCase
	endpoints := []string{
		fmt.Sprintf("api/v1/orderBookDetails?market=%s", url.QueryEscape(market)),   // Official Python SDK format (camelCase)
		fmt.Sprintf("api/v1/order_book_details?market=%s", url.QueryEscape(market)), // Legacy snake_case format
		fmt.Sprintf("/v1/orderBookDetails?market=%s", url.QueryEscape(market)),      // Legacy format with camelCase
		fmt.Sprintf("/v1/order_book_details?market=%s", url.QueryEscape(market)),    // Legacy format with snake_case
		fmt.Sprintf("api/v1/orderbook/%s", url.QueryEscape(market)),                 // Alternative format
		fmt.Sprintf("/v1/orderbook/%s", url.QueryEscape(market)),                    // Alternative legacy format
	}

	var lastErr error
	var errors []string

	for _, endpoint := range endpoints {
		resp, err := c.doRequest(ctx, "GET", endpoint, nil, authToken)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", endpoint, err))
			lastErr = err
			continue
		}

		// Read response body to check status
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to read response: %v", endpoint, readErr))
			lastErr = readErr
			continue
		}

		// Check if response is successful
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if len(body) == 0 {
				errors = append(errors, fmt.Sprintf("%s: empty response (status %d)", endpoint, resp.StatusCode))
				continue
			}
			var result json.RawMessage
			if err := json.Unmarshal(body, &result); err == nil {
				return result, nil
			}
			errors = append(errors, fmt.Sprintf("%s: parse error (status %d): %v", endpoint, resp.StatusCode, err))
		} else {
			errors = append(errors, fmt.Sprintf("%s: HTTP %d - %s", endpoint, resp.StatusCode, string(body)))
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}
	}

	// Build comprehensive error message
	errorMsg := fmt.Sprintf("failed to fetch orderbook for market %s\n\nTried endpoints: %v", market, endpoints)
	if len(errors) > 0 {
		errorMsg += "\n\nErrors:\n" + strings.Join(errors, "\n")
	}

	return nil, fmt.Errorf("%s: %w", errorMsg, lastErr)
}

// OrderBooks gets orderbook data for all markets
// Based on Python SDK: GET /api/v1/orderBooks
func (c *Client) OrderBooks(ctx context.Context, authToken string) (json.RawMessage, error) {
	// Try direct endpoints first - Python SDK uses camelCase
	endpoints := []string{
		"api/v1/orderBooks",  // Official Python SDK format (camelCase)
		"api/v1/order_books", // Legacy snake_case format
		"/v1/orderBooks",     // Legacy format with camelCase
		"/v1/order_books",    // Legacy format with snake_case
		"api/v1/orderbooks",  // Alternative format (no underscore)
		"/v1/orderbooks",     // Alternative legacy format
	}

	var lastErr error
	var errors []string

	for _, endpoint := range endpoints {
		resp, err := c.doRequest(ctx, "GET", endpoint, nil, authToken)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", endpoint, err))
			lastErr = err
			continue
		}

		// Read response body to check status
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to read response: %v", endpoint, readErr))
			lastErr = readErr
			continue
		}

		// Check if response is successful
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if len(body) == 0 {
				errors = append(errors, fmt.Sprintf("%s: empty response (status %d)", endpoint, resp.StatusCode))
				continue
			}
			var result json.RawMessage
			if err := json.Unmarshal(body, &result); err == nil {
				return result, nil
			}
			errors = append(errors, fmt.Sprintf("%s: parse error (status %d): %v", endpoint, resp.StatusCode, err))
		} else {
			errors = append(errors, fmt.Sprintf("%s: HTTP %d - %s", endpoint, resp.StatusCode, string(body)))
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}
	}

	// If all direct endpoints failed, try fetching markets and building orderbooks
	// This is a fallback approach
	marketsResp, err := c.Markets(ctx, authToken)
	if err == nil {
		// Parse markets
		var marketsData interface{}
		if err := json.Unmarshal(marketsResp, &marketsData); err == nil {
			var marketList []string

			// Extract market list from response
			if marketsMap, ok := marketsData.(map[string]interface{}); ok {
				if data, ok := marketsMap["data"].([]interface{}); ok {
					for _, item := range data {
						if marketMap, ok := item.(map[string]interface{}); ok {
							if name, ok := marketMap["name"].(string); ok {
								marketList = append(marketList, name)
							} else if symbol, ok := marketMap["symbol"].(string); ok {
								marketList = append(marketList, symbol)
							}
						} else if marketStr, ok := item.(string); ok {
							marketList = append(marketList, marketStr)
						}
					}
				} else if markets, ok := marketsMap["markets"].([]interface{}); ok {
					for _, item := range markets {
						if marketStr, ok := item.(string); ok {
							marketList = append(marketList, marketStr)
						}
					}
				}
			} else if markets, ok := marketsData.([]interface{}); ok {
				for _, item := range markets {
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

			// If we got markets, fetch orderbook for each (limit to first 10 to avoid timeout)
			if len(marketList) > 0 {
				allOrderbooks := make(map[string]interface{})
				maxMarkets := 10 // Limit to avoid timeout
				if len(marketList) > maxMarkets {
					marketList = marketList[:maxMarkets]
				}

				for _, market := range marketList {
					orderbook, err := c.OrderBookDetails(ctx, market, authToken)
					if err == nil {
						var obData interface{}
						if err := json.Unmarshal(orderbook, &obData); err == nil {
							allOrderbooks[market] = obData
						}
					}
				}

				if len(allOrderbooks) > 0 {
					result, err := json.Marshal(allOrderbooks)
					if err == nil {
						return json.RawMessage(result), nil
					}
				}
			}
		}
	}

	// Build comprehensive error message
	errorMsg := fmt.Sprintf("failed to fetch orderbooks from any endpoint\n\nTried endpoints: %v", endpoints)
	if len(errors) > 0 {
		errorMsg += "\n\nErrors:\n" + strings.Join(errors, "\n")
	}
	errorMsg += "\n\nNote: The API may not have a single endpoint for all orderbooks. Try fetching individual market orderbooks instead."

	return nil, fmt.Errorf("%s: %w", errorMsg, lastErr)
}

// Markets gets list of available markets
// Note: Python SDK doesn't have a dedicated /markets endpoint
// Markets are typically extracted from orderBooks response
func (c *Client) Markets(ctx context.Context, authToken string) (json.RawMessage, error) {
	// Try various endpoint formats (though Python SDK doesn't list this endpoint)
	endpoints := []string{
		"api/v1/markets",      // Try standard format
		"/v1/markets",         // Legacy format
		"api/v1/markets/list", // Alternative format
		"/v1/markets/list",    // Alternative legacy format
	}

	var lastErr error
	for _, endpoint := range endpoints {
		resp, err := c.doRequest(ctx, "GET", endpoint, nil, authToken)
		if err != nil {
			lastErr = err
			continue
		}

		var result json.RawMessage
		if err := c.parseResponse(resp, &result); err != nil {
			lastErr = err
			continue
		}

		return result, nil
	}

	return nil, fmt.Errorf("failed to fetch markets from any endpoint: %w", lastErr)
}

// AccountData gets account data by L1 address or index
// The API requires a "by" parameter to specify the query method
// Valid values: "index" or "l1_address"
func (c *Client) AccountData(ctx context.Context, l1Address string, accountIndex *uint32, authToken string) (json.RawMessage, error) {
	// Build query parameters
	// The API requires "by" parameter: "index" (not "account_index") or "l1_address"
	q := url.Values{}

	if l1Address != "" {
		q.Set("by", "l1_address")
		q.Set("value", l1Address)
	} else if accountIndex != nil {
		// accountIndex can be 0 (valid), so handle it
		q.Set("by", "index")
		q.Set("value", fmt.Sprintf("%d", *accountIndex))
	} else {
		// If no parameters provided, try with account_index from token
		// Parse token to get account_index
		if authToken != "" {
			tokenParts := strings.Split(authToken, ":")
			if len(tokenParts) >= 2 {
				// Use account_index from token (even if 0)
				q.Set("by", "index")
				q.Set("value", tokenParts[1])
			} else {
				return nil, fmt.Errorf("cannot determine account_index: no accountIndex parameter and token format invalid")
			}
		} else {
			return nil, fmt.Errorf("account endpoint requires either l1_address or account_index parameter, and auth token for account_index lookup")
		}
	}

	queryString := q.Encode()

	// Ensure we always have a query string
	if queryString == "" {
		return nil, fmt.Errorf("internal error: query string is empty but should have 'by' parameter set (l1Address=%q, accountIndex=%v, authToken=%q)",
			l1Address, accountIndex, func() string {
				if authToken == "" {
					return "empty"
				}
				if len(authToken) > 20 {
					return authToken[:20] + "..."
				}
				return authToken
			}())
	}

	// Try official SDK endpoint format first (api/v1/...)
	endpoints := []string{
		"api/v1/account", // Official SDK format
		"/v1/account",    // Legacy format
		"/v1/accounts",   // Alternative legacy format
	}

	var errors []string
	var lastErr error

	for _, baseEndpoint := range endpoints {
		endpoint := baseEndpoint
		if queryString != "" {
			endpoint += "?" + queryString
		}

		resp, err := c.doRequest(ctx, "GET", endpoint, nil, authToken)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: request failed: %v", endpoint, err))
			lastErr = err
			continue
		}

		// Read response body before checking status
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to read response: %v", endpoint, readErr))
			lastErr = readErr
			continue
		}

		// Check if response is successful
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if len(body) == 0 {
				errors = append(errors, fmt.Sprintf("%s: empty response (status %d)", endpoint, resp.StatusCode))
				continue
			}
			var result json.RawMessage
			if err := json.Unmarshal(body, &result); err == nil {
				return result, nil
			}
			// If parsing failed, log and try next endpoint
			errors = append(errors, fmt.Sprintf("%s: parse error (status %d): %v (body: %s)", endpoint, resp.StatusCode, err, string(body)))
		} else {
			// Log error response
			errors = append(errors, fmt.Sprintf("%s: API error (status %d): %s", endpoint, resp.StatusCode, string(body)))
			lastErr = fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
		}
	}

	// Build comprehensive error message
	errorMsg := fmt.Sprintf("failed to fetch account from any endpoint (tried: %v)", endpoints)
	if len(errors) > 0 {
		errorMsg += "\n\nErrors:\n" + strings.Join(errors, "\n")
	}
	if authToken == "" {
		errorMsg += "\n\nNote: No auth token provided. Account endpoint may require authentication."
	} else {
		// Parse token to show account_index from token
		tokenParts := strings.Split(authToken, ":")
		if len(tokenParts) >= 3 {
			errorMsg += fmt.Sprintf("\n\nAuth token provided: deadline=%s, account_index=%s, api_key_index=%s (token length: %d)",
				tokenParts[0], tokenParts[1], tokenParts[2], len(authToken))
			if tokenParts[1] == "0" && (accountIndex == nil || *accountIndex == 0) {
				errorMsg += "\n\n⚠️ WARNING: Account index in token is 0. If this is incorrect, check LIGHTER_ACCOUNT_INDEX environment variable."
			}
		} else {
			// Sanitize token for logging (show first 20 chars)
			tokenPreview := authToken
			if len(tokenPreview) > 20 {
				tokenPreview = tokenPreview[:20] + "..."
			}
			errorMsg += fmt.Sprintf("\n\nAuth token provided: %s (length: %d)", tokenPreview, len(authToken))
		}
	}
	if accountIndex != nil && *accountIndex == 0 {
		errorMsg += "\n\nNote: account_index=0 was not passed as query parameter (may be invalid)"
	}

	if lastErr != nil {
		return nil, fmt.Errorf("%s: %w", errorMsg, lastErr)
	}
	return nil, fmt.Errorf("%s", errorMsg)
}

// AccountsByL1Address gets all accounts for an L1 address
func (c *Client) AccountsByL1Address(ctx context.Context, l1Address string, authToken string) (json.RawMessage, error) {
	// Try multiple endpoint formats
	endpoints := []string{
		fmt.Sprintf("api/v1/accounts_by_l1_address?l1_address=%s", url.QueryEscape(l1Address)),
		fmt.Sprintf("/v1/accounts_by_l1_address?l1_address=%s", url.QueryEscape(l1Address)),
		fmt.Sprintf("api/v1/accounts?l1_address=%s", url.QueryEscape(l1Address)),
		fmt.Sprintf("/v1/accounts?l1_address=%s", url.QueryEscape(l1Address)),
	}

	var lastErr error
	for _, endpoint := range endpoints {
		resp, err := c.doRequest(ctx, "GET", endpoint, nil, authToken)
		if err != nil {
			lastErr = err
			continue
		}

		// Read response body
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr != nil {
			lastErr = readErr
			continue
		}

		// Check if response is successful
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var result json.RawMessage
			if err := json.Unmarshal(body, &result); err == nil {
				return result, nil
			}
			lastErr = fmt.Errorf("parse error: %v", err)
		} else {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}
	}

	return nil, fmt.Errorf("failed to fetch accounts from any endpoint: %w", lastErr)
}

// APIKeys gets API key data for an account
func (c *Client) APIKeys(ctx context.Context, accountIndex uint32, apiKeyIndex uint32, authToken string) (json.RawMessage, error) {
	endpoint := fmt.Sprintf("/v1/apikeys?account_index=%d&api_key_index=%d", accountIndex, apiKeyIndex)
	resp, err := c.doRequest(ctx, "GET", endpoint, nil, authToken)
	if err != nil {
		return nil, err
	}

	var result json.RawMessage
	if err := c.parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// Pools gets all public pools
func (c *Client) Pools(ctx context.Context, authToken string) (json.RawMessage, error) {
	// Try multiple possible endpoints
	endpoints := []string{"/v1/pools", "/v1/public_pools"}

	for _, endpoint := range endpoints {
		resp, err := c.doRequest(ctx, "GET", endpoint, nil, authToken)
		if err == nil {
			var result json.RawMessage
			if err := c.parseResponse(resp, &result); err == nil {
				return result, nil
			}
		}
	}

	// If all endpoints fail, return error
	return nil, fmt.Errorf("failed to fetch pools from any endpoint")
}

// AccountActiveOrders gets active orders for an account
// Based on Python SDK: GET /api/v1/accountActiveOrders
// Requires: account_index, market_id
// Auth token should be passed as query parameter 'auth' (Python SDK format)
func (c *Client) AccountActiveOrders(ctx context.Context, accountIndex uint32, marketID uint8, authToken string) (json.RawMessage, error) {
	// Python SDK format: /api/v1/accountActiveOrders?account_index=X&market_id=Y&auth=TOKEN
	// The auth token goes in the query parameter, not Authorization header for these endpoints
	q := url.Values{}
	q.Set("account_index", fmt.Sprintf("%d", accountIndex))
	q.Set("market_id", fmt.Sprintf("%d", marketID))
	if authToken != "" {
		q.Set("auth", authToken)
	}

	endpoint := fmt.Sprintf("api/v1/accountActiveOrders?%s", q.Encode())
	// Don't pass authToken to doRequest - it's already in the query parameter
	return c.singleRequest(ctx, "GET", endpoint, nil, "")
}

// AccountInactiveOrders gets inactive orders for an account
// Based on Python SDK: GET /api/v1/accountInactiveOrders
// Requires: account_index, limit
// Auth token should be passed as query parameter 'auth' (Python SDK format)
func (c *Client) AccountInactiveOrders(ctx context.Context, accountIndex uint32, limit int, authToken string, marketID *uint8) (json.RawMessage, error) {
	// Python SDK format: /api/v1/accountInactiveOrders?account_index=X&limit=Y&auth=TOKEN
	// The auth token goes in the query parameter, not Authorization header for these endpoints
	q := url.Values{}
	q.Set("account_index", fmt.Sprintf("%d", accountIndex))
	q.Set("limit", fmt.Sprintf("%d", limit))
	if authToken != "" {
		q.Set("auth", authToken)
	}
	if marketID != nil {
		q.Set("market_id", fmt.Sprintf("%d", *marketID))
	}

	endpoint := fmt.Sprintf("api/v1/accountInactiveOrders?%s", q.Encode())
	// Don't pass authToken to doRequest - it's already in the query parameter
	return c.singleRequest(ctx, "GET", endpoint, nil, "")
}

// Orders gets all orders for an account (both active and inactive)
// This is a convenience wrapper that tries both endpoints
func (c *Client) Orders(ctx context.Context, accountIndex *uint32, authToken string) (json.RawMessage, error) {
	if accountIndex == nil {
		return nil, fmt.Errorf("account_index is required")
	}

	// Try to get active orders first (for all markets, we'll need to iterate or use market_id=255 for all)
	// For now, try market_id=255 (all markets) based on Python SDK defaults
	activeOrders, err := c.AccountActiveOrders(ctx, *accountIndex, 255, authToken)
	if err == nil {
		return activeOrders, nil
	}

	// If active orders fails, try inactive orders with a reasonable limit
	inactiveOrders, err2 := c.AccountInactiveOrders(ctx, *accountIndex, 100, authToken, nil)
	if err2 == nil {
		return inactiveOrders, nil
	}

	return nil, fmt.Errorf("failed to fetch orders: active orders error: %v, inactive orders error: %v", err, err2)
}

// singleRequest performs a single HTTP request and returns the response body
func (c *Client) singleRequest(ctx context.Context, method, endpoint string, body io.Reader, authToken string) (json.RawMessage, error) {
	resp, err := c.doRequest(ctx, method, endpoint, body, authToken)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return json.RawMessage(bodyBytes), nil
}

// Pool gets a specific pool by address
func (c *Client) Pool(ctx context.Context, poolAddress string, authToken string) (json.RawMessage, error) {
	endpoint := fmt.Sprintf("/v1/pools/%s", url.QueryEscape(poolAddress))
	resp, err := c.doRequest(ctx, "GET", endpoint, nil, authToken)
	if err != nil {
		return nil, err
	}

	var result json.RawMessage
	if err := c.parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// OrderBookOrders gets orderbook orders (bids/asks) for a specific market
// Based on Python SDK: GET /api/v1/orderBookOrders
// Note: This endpoint requires market_id and limit parameters
func (c *Client) OrderBookOrders(ctx context.Context, marketID uint32, authToken string) (json.RawMessage, error) {
	// API requires limit parameter (default to 20 for best bid/ask)
	limit := 20
	// Try multiple endpoint formats - API requires market_id and limit parameters
	endpoints := []string{
		fmt.Sprintf("api/v1/orderBookOrders?market_id=%d&limit=%d", marketID, limit),   // Official format
		fmt.Sprintf("api/v1/orderBookOrders?market_id=%d", marketID),                   // Try without limit first
		fmt.Sprintf("api/v1/order_book_orders?market_id=%d&limit=%d", marketID, limit), // Legacy snake_case format
		fmt.Sprintf("/v1/orderBookOrders?market_id=%d&limit=%d", marketID, limit),      // Legacy format with camelCase
		fmt.Sprintf("/v1/order_book_orders?market_id=%d&limit=%d", marketID, limit),    // Legacy format with snake_case
	}

	var lastErr error
	var errors []string

	for _, endpoint := range endpoints {
		resp, err := c.doRequest(ctx, "GET", endpoint, nil, authToken)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", endpoint, err))
			lastErr = err
			continue
		}

		// Read response body to check status
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if readErr != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to read response: %v", endpoint, readErr))
			lastErr = readErr
			continue
		}

		// Check if response is successful
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if len(body) == 0 {
				errors = append(errors, fmt.Sprintf("%s: empty response (status %d)", endpoint, resp.StatusCode))
				continue
			}
			var result json.RawMessage
			if err := json.Unmarshal(body, &result); err == nil {
				return result, nil
			}
			errors = append(errors, fmt.Sprintf("%s: parse error (status %d): %v", endpoint, resp.StatusCode, err))
		} else {
			errors = append(errors, fmt.Sprintf("%s: HTTP %d - %s", endpoint, resp.StatusCode, string(body)))
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}
	}

	// Build comprehensive error message
	errorMsg := fmt.Sprintf("failed to fetch orderbook orders for market_id %d\n\nTried endpoints: %v", marketID, endpoints)
	if len(errors) > 0 {
		errorMsg += "\n\nErrors:\n" + strings.Join(errors, "\n")
	}

	return nil, fmt.Errorf("%s: %w", errorMsg, lastErr)
}
