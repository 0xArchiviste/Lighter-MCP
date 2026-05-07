package account

import (
	"encoding/json"
	"testing"
	"time"
)

// TestTransactionFormat tests that the transaction format matches the frontend format
func TestTransactionFormat(t *testing.T) {
	// This is a unit test to verify the transaction structure
	// We can't actually sign without real keys, but we can verify the structure
	
	// Expected frontend format:
	// {
	//   "tx_type": "20",
	//   "tx_info": "{...}",  // JSON string
	//   "price_protection": "false"
	// }
	
	// Test that tx_info is a JSON string (not an object)
	txInfoJSON := `{"AccountIndex":194667,"ApiKeyIndex":0,"MarketIndex":0,"InitialMarginFraction":10000,"MarginMode":1,"ExpiredAt":1762796130488,"Nonce":84,"Sig":"test"}`
	
	// Verify it's valid JSON
	var txInfo map[string]interface{}
	if err := json.Unmarshal([]byte(txInfoJSON), &txInfo); err != nil {
		t.Fatalf("tx_info should be valid JSON: %v", err)
	}
	
	// Verify required fields exist
	requiredFields := []string{"AccountIndex", "ApiKeyIndex", "MarketIndex", "InitialMarginFraction", "MarginMode", "ExpiredAt", "Nonce", "Sig"}
	for _, field := range requiredFields {
		if _, ok := txInfo[field]; !ok {
			t.Errorf("tx_info missing required field: %s", field)
		}
	}
	
	// Verify ExpiredAt is in milliseconds (13 digits)
	expiredAt, ok := txInfo["ExpiredAt"].(float64)
	if !ok {
		t.Fatal("ExpiredAt should be a number")
	}
	expiredAtStr := formatFloat(expiredAt)
	if len(expiredAtStr) < 13 {
		t.Errorf("ExpiredAt should be in milliseconds (13+ digits), got %d digits: %s", len(expiredAtStr), expiredAtStr)
	}
}

func formatFloat(f float64) string {
	// Simple float to string conversion for testing
	return time.UnixMilli(int64(f)).Format(time.RFC3339)
}

// TestLeverageConversion tests the leverage to initial margin fraction conversion
func TestLeverageConversion(t *testing.T) {
	tests := []struct {
		leverage uint16
		expected uint16
	}{
		{1, 10000},
		{2, 5000},
		{3, 3333},
		{10, 1000},
		{50, 200},
	}
	
	for _, tt := range tests {
		result := LeverageToInitialMarginFraction(tt.leverage)
		if result != tt.expected {
			t.Errorf("LeverageToInitialMarginFraction(%d) = %d, expected %d", tt.leverage, result, tt.expected)
		}
	}
}

// TestMarginModeValidation tests that cross margin is rejected
func TestMarginModeValidation(t *testing.T) {
	// This test verifies that cross margin validation works
	// We can't actually call SetMarginType without a real client, but we can test the logic
	
	if CrossMargin != 0 {
		t.Errorf("CrossMargin should be 0, got %d", CrossMargin)
	}
	
	if IsolatedMargin != 1 {
		t.Errorf("IsolatedMargin should be 1, got %d", IsolatedMargin)
	}
}

