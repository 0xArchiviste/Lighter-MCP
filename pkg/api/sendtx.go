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
)

// SendTxWithAuth sends a single signed transaction with authentication
func (c *Client) SendTxWithAuth(ctx context.Context, tx SignedTx, authToken string) (string, error) {
	// If payload is a map, try flattening it (merge payload fields into top level)
	var body []byte
	var err error
	
	if payloadMap, ok := tx.Payload.(map[string]interface{}); ok {
		// Try sending the payload directly first (might be Format 1 from margin.go)
		// If it has "Sig" field, try converting it to "signature" and adding tx_type
		if sigVal, hasSig := payloadMap["Sig"]; hasSig {
			// Format 1: Use payload directly but replace Sig with signature and add tx_type
			directPayload := make(map[string]interface{})
			for k, v := range payloadMap {
				if k == "Sig" {
					directPayload["signature"] = sigVal
				} else {
					directPayload[k] = v
				}
			}
			directPayload["tx_type"] = tx.TxType
			body, err = json.Marshal(directPayload)
		} else {
			// Format 2: Flattened structure (all fields at top level) with snake_case
			flatTx := make(map[string]interface{})
			for k, v := range payloadMap {
				// Skip tx_type and signature if they're in payload (we'll use top-level values)
				if k == "tx_type" || k == "TxType" || k == "signature" || k == "Signature" {
					continue
				}
				// Convert CamelCase to snake_case
				snakeKey := toSnakeCase(k)
				flatTx[snakeKey] = v
			}
			// Add signature and tx_type at top level
			flatTx["signature"] = tx.Signature
			flatTx["tx_type"] = tx.TxType
			body, err = json.Marshal(flatTx)
		}
	} else {
		// Use original structure
		body, err = json.Marshal(tx)
	}
	
	if err != nil {
		return "", fmt.Errorf("failed to marshal tx: %w", err)
	}
	
	// Debug: log the actual payload being sent
	fmt.Fprintf(os.Stderr, "[DEBUG] Transaction payload (with auth): %s\n", string(body))
	
	// Try multiple endpoint formats (official SDK format first)
	endpoints := []string{
		"api/v1/sendTx",  // Official SDK format (camelCase)
		"api/v1/send_tx", // Snake case format
		"/api/v1/sendTx", // Alternative format
		"/api/v1/send_tx", // Alternative snake case
		"/v1/send_tx",     // Legacy format
	}

	var errors []string
	for _, endpoint := range endpoints {
		// Build full URL
		var fullURL string
		if strings.HasPrefix(endpoint, "api/") {
			baseURL := strings.TrimSuffix(c.baseURL, "/")
			fullURL = fmt.Sprintf("%s/%s", baseURL, endpoint)
		} else {
			baseURL := strings.TrimSuffix(c.baseURL, "/")
			fullURL = fmt.Sprintf("%s%s", baseURL, endpoint)
		}
		
		// Create request with JSON content type
		req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewBuffer(body))
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to create request: %v", endpoint, err))
			continue
		}
		
		req.Header.Set("Content-Type", "application/json")
		
		// Set authorization header
		if authToken != "" {
			c.setAuthHeader(req, authToken)
		}
		
		resp, err := c.client.Do(req)
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
				// Log the full error for debugging
				fmt.Fprintf(os.Stderr, "[DEBUG] Endpoint %s returned HTTP %d: %s\n", endpoint, resp.StatusCode, string(bodyBytes))
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

// SendTxRaw sends a raw transaction payload (map) directly
// The API expects application/x-www-form-urlencoded format, not JSON!
func (c *Client) SendTxRaw(ctx context.Context, txPayload map[string]interface{}, authToken string) (string, error) {
	// Extract values
	txType := txPayload["tx_type"].(string)
	txInfo := txPayload["tx_info"].(string)
	priceProtection := txPayload["price_protection"].(string)
	
	// Build form-encoded data (matching frontend format)
	formData := url.Values{}
	formData.Set("tx_type", txType)
	formData.Set("tx_info", txInfo) // tx_info is already a JSON string, just set it directly
	formData.Set("price_protection", priceProtection)
	
	body := []byte(formData.Encode())
	
	// Debug: log the actual payload being sent
	fmt.Fprintf(os.Stderr, "[DEBUG] Raw transaction payload (form-encoded): %s\n", string(body))

	// Try multiple endpoint formats (official SDK format first)
	endpoints := []string{
		"api/v1/sendTx",  // Official SDK format (camelCase)
		"api/v1/send_tx", // Snake case format
		"/api/v1/sendTx", // Alternative format
		"/api/v1/send_tx", // Alternative snake case
		"/v1/send_tx",     // Legacy format
	}

	var errors []string
	for _, endpoint := range endpoints {
		// Build full URL
		var fullURL string
		if strings.HasPrefix(endpoint, "api/") {
			baseURL := strings.TrimSuffix(c.baseURL, "/")
			fullURL = fmt.Sprintf("%s/%s", baseURL, endpoint)
		} else {
			baseURL := strings.TrimSuffix(c.baseURL, "/")
			fullURL = fmt.Sprintf("%s%s", baseURL, endpoint)
		}
		
		// Create request with form-encoded content type (matching frontend)
		req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewBuffer(body))
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to create request: %v", endpoint, err))
			continue
		}
		
		// Set form-encoded content type (matching frontend format)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
		
		// Set authorization header
		if authToken != "" {
			// Use the setAuthHeader method from the Client
			// We need to access it through the client's method
			// For now, set it directly
			if strings.Contains(authToken, ":") {
				req.Header.Set("Authorization", authToken)
			} else {
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))
			}
		}
		
		resp, err := c.client.Do(req)
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
				// Log the full error for debugging
				fmt.Fprintf(os.Stderr, "[DEBUG] Endpoint %s returned HTTP %d: %s\n", endpoint, resp.StatusCode, string(bodyBytes))
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

// SendTxBatchRaw sends multiple raw transaction payloads (form-encoded) in batch
// The API expects application/x-www-form-urlencoded format for batch transactions
func (c *Client) SendTxBatchRaw(ctx context.Context, txPayloads []map[string]interface{}, authToken string) ([]string, error) {
	if len(txPayloads) == 0 {
		return nil, fmt.Errorf("no transactions to send")
	}

	// Build form-encoded data for batch
	// Format: tx_type[], tx_info[], price_protection[] (arrays)
	formData := url.Values{}
	for _, txPayload := range txPayloads {
		txType := txPayload["tx_type"].(string)
		txInfo := txPayload["tx_info"].(string)
		priceProtection := txPayload["price_protection"].(string)
		
		formData.Add("tx_type", txType)
		formData.Add("tx_info", txInfo)
		formData.Add("price_protection", priceProtection)
	}
	
	body := []byte(formData.Encode())
	
	// Debug: log the batch payload
	fmt.Fprintf(os.Stderr, "[DEBUG] Batch transaction payload (form-encoded): %d transactions\n", len(txPayloads))
	
	// Try multiple endpoint formats for batch
	endpoints := []string{
		"api/v1/sendTxBatch",  // Official SDK format (camelCase)
		"api/v1/send_tx_batch", // Snake case format
		"/api/v1/sendTxBatch", // Alternative format
		"/api/v1/send_tx_batch", // Alternative snake case
		"/v1/send_tx_batch",     // Legacy format
	}

	var errors []string
	for _, endpoint := range endpoints {
		// Build full URL
		var fullURL string
		if strings.HasPrefix(endpoint, "api/") {
			baseURL := strings.TrimSuffix(c.baseURL, "/")
			fullURL = fmt.Sprintf("%s/%s", baseURL, endpoint)
		} else {
			baseURL := strings.TrimSuffix(c.baseURL, "/")
			fullURL = fmt.Sprintf("%s%s", baseURL, endpoint)
		}
		
		// Create request with form-encoded content type
		req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewBuffer(body))
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to create request: %v", endpoint, err))
			continue
		}
		
		// Set form-encoded content type
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
		
		// Set authorization header
		if authToken != "" {
			if strings.Contains(authToken, ":") {
				req.Header.Set("Authorization", authToken)
			} else {
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))
			}
		}
		
		resp, err := c.client.Do(req)
		if err == nil {
			// Read response body
			bodyBytes, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			
			if readErr != nil {
				errors = append(errors, fmt.Sprintf("%s: failed to read response: %v", endpoint, readErr))
				continue
			}
			
			// Check status code
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				fmt.Fprintf(os.Stderr, "[DEBUG] Batch endpoint %s returned HTTP %d: %s\n", endpoint, resp.StatusCode, string(bodyBytes))
				errors = append(errors, fmt.Sprintf("%s: HTTP %d - %s", endpoint, resp.StatusCode, string(bodyBytes)))
				continue
			}
			
			// Try to parse response (array of tx hashes or wrapped)
			var result struct {
				TxHashes []string `json:"tx_hashes"`
			}
			if err := json.Unmarshal(bodyBytes, &result); err == nil && len(result.TxHashes) > 0 {
				return result.TxHashes, nil
			}
			
			// Try alternative structure
			var wrappedResult struct {
				Data struct {
					TxHashes []string `json:"tx_hashes"`
				} `json:"data"`
			}
			if err := json.Unmarshal(bodyBytes, &wrappedResult); err == nil && len(wrappedResult.Data.TxHashes) > 0 {
				return wrappedResult.Data.TxHashes, nil
			}
			
			// Try direct array
			var directHashes []string
			if err := json.Unmarshal(bodyBytes, &directHashes); err == nil && len(directHashes) > 0 {
				return directHashes, nil
			}
			
			errors = append(errors, fmt.Sprintf("%s: unexpected response format: %s", endpoint, string(bodyBytes)))
		} else {
			errors = append(errors, fmt.Sprintf("%s: %v", endpoint, err))
		}
	}

	return nil, fmt.Errorf("failed to send batch transaction from any endpoint: %s", strings.Join(errors, "; "))
}
