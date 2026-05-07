package indicators

import (
	"context"
	"fmt"
	"math"

	"github.com/cinar/indicator"
	"github.com/0xarchiviste/lighter-mcp/pkg/api"
	"github.com/0xarchiviste/lighter-mcp/pkg/markets"
)

// IndicatorService provides technical indicator calculations
type IndicatorService struct {
	apiClient *api.Client
}

// NewIndicatorService creates a new indicator service
func NewIndicatorService(apiClient *api.Client) *IndicatorService {
	return &IndicatorService{
		apiClient: apiClient,
	}
}

// IndicatorResult holds the result of an indicator calculation
type IndicatorResult struct {
	Indicator string                 `json:"indicator"`
	Market    string                 `json:"market"`
	Value     float64                `json:"value"`
	Signal    string                 `json:"signal"` // "buy", "sell", "hold"
	Metadata  map[string]interface{} `json:"metadata"`
}

// CalculateRSI calculates the Relative Strength Index for a market
// RSI > 70 = overbought, RSI < 30 = oversold
func (s *IndicatorService) CalculateRSI(ctx context.Context, marketSymbol string, period int, authToken string) (*IndicatorResult, error) {
	// Fetch market data
	prices, err := s.fetchPriceHistory(ctx, marketSymbol, period+14, authToken) // Need extra data for RSI calculation
	if err != nil {
		return nil, err
	}

	if len(prices) < period {
		return nil, fmt.Errorf("insufficient data: need at least %d prices, got %d", period, len(prices))
	}

	// Calculate RSI using indicator package
	rsi, _ := indicator.Rsi(prices)
	if len(rsi) < period {
		return nil, fmt.Errorf("insufficient RSI data")
	}
	currentRSI := rsi[len(rsi)-1]

	// Determine signal
	signal := "hold"
	if currentRSI > 70 {
		signal = "sell" // Overbought
	} else if currentRSI < 30 {
		signal = "buy" // Oversold
	}

	return &IndicatorResult{
		Indicator: "RSI",
		Market:    marketSymbol,
		Value:     currentRSI,
		Signal:    signal,
		Metadata: map[string]interface{}{
			"period":     period,
			"overbought": currentRSI > 70,
			"oversold":   currentRSI < 30,
		},
	}, nil
}

// CalculateMACD calculates the Moving Average Convergence Divergence
func (s *IndicatorService) CalculateMACD(ctx context.Context, marketSymbol string, authToken string) (*IndicatorResult, error) {
	// MACD typically uses 26-day period, so need at least 26 prices
	prices, err := s.fetchPriceHistory(ctx, marketSymbol, 50, authToken)
	if err != nil {
		return nil, err
	}

	// Calculate MACD - uses standard parameters (12, 26, 9)
	macdLine, signalLine := indicator.Macd(prices)
	if len(macdLine) == 0 || len(signalLine) == 0 {
		return nil, fmt.Errorf("failed to calculate MACD")
	}

	currentMACD := macdLine[len(macdLine)-1]
	currentSignalLine := signalLine[len(signalLine)-1]
	currentHistogram := currentMACD - currentSignalLine

	// Determine trading signal based on MACD crossover
	tradingSignal := "hold"
	if currentHistogram > 0 && currentMACD > currentSignalLine {
		tradingSignal = "buy" // Bullish crossover
	} else if currentHistogram < 0 && currentMACD < currentSignalLine {
		tradingSignal = "sell" // Bearish crossover
	}

	return &IndicatorResult{
		Indicator: "MACD",
		Market:    marketSymbol,
		Value:     currentMACD,
		Signal:    tradingSignal,
		Metadata: map[string]interface{}{
			"macd":      currentMACD,
			"signal":    currentSignalLine,
			"histogram": currentHistogram,
			"bullish":   currentHistogram > 0,
		},
	}, nil
}

// CalculateMovingAverage calculates Simple Moving Average
func (s *IndicatorService) CalculateMovingAverage(ctx context.Context, marketSymbol string, period int, authToken string) (*IndicatorResult, error) {
	prices, err := s.fetchPriceHistory(ctx, marketSymbol, period+5, authToken)
	if err != nil {
		return nil, err
	}

	// Calculate SMA
	ma := indicator.Sma(period, prices)
	if len(ma) == 0 {
		return nil, fmt.Errorf("failed to calculate SMA")
	}
	currentMA := ma[len(ma)-1]
	currentPrice := prices[len(prices)-1]

	// Determine signal based on price vs MA
	signal := "hold"
	if currentPrice > currentMA*1.02 { // Price 2% above MA
		signal = "buy"
	} else if currentPrice < currentMA*0.98 { // Price 2% below MA
		signal = "sell"
	}

	return &IndicatorResult{
		Indicator: "SMA",
		Market:    marketSymbol,
		Value:     currentMA,
		Signal:    signal,
		Metadata: map[string]interface{}{
			"period":        period,
			"current_price": currentPrice,
			"ma_value":      currentMA,
			"deviation":     ((currentPrice - currentMA) / currentMA) * 100,
		},
	}, nil
}

// CalculateBollingerBands calculates Bollinger Bands
func (s *IndicatorService) CalculateBollingerBands(ctx context.Context, marketSymbol string, period int, authToken string) (*IndicatorResult, error) {
	prices, err := s.fetchPriceHistory(ctx, marketSymbol, period+5, authToken)
	if err != nil {
		return nil, err
	}

	// Calculate Bollinger Bands
	middle, upper, lower := indicator.BollingerBands(prices)

	currentPrice := prices[len(prices)-1]
	currentMiddle := middle[len(middle)-1]
	currentUpper := upper[len(upper)-1]
	currentLower := lower[len(lower)-1]

	// Determine signal
	signal := "hold"
	if currentPrice >= currentUpper {
		signal = "sell" // Price at upper band (overbought)
	} else if currentPrice <= currentLower {
		signal = "buy" // Price at lower band (oversold)
	}

	return &IndicatorResult{
		Indicator: "Bollinger Bands",
		Market:    marketSymbol,
		Value:     currentMiddle,
		Signal:    signal,
		Metadata: map[string]interface{}{
			"period":        period,
			"current_price": currentPrice,
			"upper_band":    currentUpper,
			"middle_band":   currentMiddle,
			"lower_band":    currentLower,
			"bandwidth":     ((currentUpper - currentLower) / currentMiddle) * 100,
		},
	}, nil
}

// CalculateEMA calculates Exponential Moving Average
func (s *IndicatorService) CalculateEMA(ctx context.Context, marketSymbol string, period int, authToken string) (*IndicatorResult, error) {
	prices, err := s.fetchPriceHistory(ctx, marketSymbol, period*2, authToken)
	if err != nil {
		return nil, err
	}

	ema := indicator.Ema(period, prices)
	currentEMA := ema[len(ema)-1]
	currentPrice := prices[len(prices)-1]

	// Determine signal
	signal := "hold"
	if currentPrice > currentEMA {
		signal = "buy"
	} else if currentPrice < currentEMA {
		signal = "sell"
	}

	return &IndicatorResult{
		Indicator: "EMA",
		Market:    marketSymbol,
		Value:     currentEMA,
		Signal:    signal,
		Metadata: map[string]interface{}{
			"period":        period,
			"current_price": currentPrice,
			"ema_value":     currentEMA,
		},
	}, nil
}

// CalculateATR calculates Average True Range (volatility indicator)
func (s *IndicatorService) CalculateATR(ctx context.Context, marketSymbol string, period int, authToken string) (*IndicatorResult, error) {
	// ATR requires high, low, close prices
	// For now, use price history as close and simulate high/low
	prices, err := s.fetchPriceHistory(ctx, marketSymbol, period+5, authToken)
	if err != nil {
		return nil, err
	}

	// Simulate high/low (±1% from close)
	high := make([]float64, len(prices))
	low := make([]float64, len(prices))
	for i, p := range prices {
		high[i] = p * 1.01
		low[i] = p * 0.99
	}

	atr, _ := indicator.Atr(period, high, low, prices)
	if len(atr) == 0 {
		return nil, fmt.Errorf("failed to calculate ATR")
	}
	currentATR := atr[len(atr)-1]

	return &IndicatorResult{
		Indicator: "ATR",
		Market:    marketSymbol,
		Value:     currentATR,
		Signal:    "info", // ATR is not directional
		Metadata: map[string]interface{}{
			"period":     period,
			"volatility": "high", // TODO: Classify based on historical ATR
		},
	}, nil
}

// fetchPriceHistory fetches historical price data for a market
// For now, this generates sample data. In production, integrate with historical data API.
func (s *IndicatorService) fetchPriceHistory(ctx context.Context, marketSymbol string, periods int, authToken string) ([]float64, error) {
	// Fetch current market price
	marketsClient := markets.NewClient(s.apiClient)
	market, err := marketsClient.GetMarketWithPrices(ctx, marketSymbol, authToken)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch market data: %w", err)
	}

	currentPrice := 3000.0 // Default price for demo
	if market.CurrentPrice != nil {
		currentPrice, _ = market.CurrentPrice.Float64()
	}
	if currentPrice == 0 {
		return nil, fmt.Errorf("invalid price: %f", currentPrice)
	}

	// Generate sample historical data (TODO: Replace with real API data)
	prices := make([]float64, periods)
	for i := 0; i < periods; i++ {
		// Simulate price movement (random walk with slight upward trend)
		variation := math.Sin(float64(i)*0.3) * 0.02 * currentPrice
		trend := float64(i) * 0.001 * currentPrice
		prices[i] = currentPrice + variation - trend
	}

	return prices, nil
}

// CalculateAllIndicators calculates all available indicators for a market
func (s *IndicatorService) CalculateAllIndicators(ctx context.Context, marketSymbol string, authToken string) ([]*IndicatorResult, error) {
	var results []*IndicatorResult

	// RSI (14-period)
	if rsi, err := s.CalculateRSI(ctx, marketSymbol, 14, authToken); err == nil {
		results = append(results, rsi)
	}

	// MACD
	if macd, err := s.CalculateMACD(ctx, marketSymbol, authToken); err == nil {
		results = append(results, macd)
	}

	// SMA (20-period)
	if sma, err := s.CalculateMovingAverage(ctx, marketSymbol, 20, authToken); err == nil {
		results = append(results, sma)
	}

	// EMA (20-period)
	if ema, err := s.CalculateEMA(ctx, marketSymbol, 20, authToken); err == nil {
		results = append(results, ema)
	}

	// Bollinger Bands (20-period)
	if bb, err := s.CalculateBollingerBands(ctx, marketSymbol, 20, authToken); err == nil {
		results = append(results, bb)
	}

	// ATR (14-period)
	if atr, err := s.CalculateATR(ctx, marketSymbol, 14, authToken); err == nil {
		results = append(results, atr)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("failed to calculate any indicators")
	}

	return results, nil
}

