package account

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/0xarchiviste/lighter-mcp/pkg/api"
	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/markets"
	lighterclient "github.com/elliottech/lighter-go/client"
	lightertypes "github.com/elliottech/lighter-go/types"
	lightertxtypes "github.com/elliottech/lighter-go/types/txtypes"
)

// OrderSide represents order side
type OrderSide string

const (
	OrderSideBuy  OrderSide = "buy"
	OrderSideSell OrderSide = "sell"
)

// OrderType represents order type
type OrderType string

const (
	OrderTypeLimit           OrderType = "limit"
	OrderTypeMarket          OrderType = "market"
	OrderTypeStopLoss        OrderType = "stop-loss"
	OrderTypeStopLossLimit   OrderType = "stop-loss-limit"
	OrderTypeTakeProfit      OrderType = "take-profit"
	OrderTypeTakeProfitLimit OrderType = "take-profit-limit"
	OrderTypeTWAP            OrderType = "twap"
)

// TimeInForce represents time in force
type TimeInForce string

const (
	TIFGTT TimeInForce = "good-till-time"      // Good Till Time
	TIFGTC TimeInForce = "good-till-cancel"    // Good Till Cancel (same as GTT but commonly called GTC)
	TIFIOC TimeInForce = "immediate-or-cancel" // Immediate Or Cancel
	TIFPO  TimeInForce = "post-only"           // Post Only
)

// OrderPlacementClient provides order placement and cancellation operations
type OrderPlacementClient struct {
	apiClient *api.Client
	cfg       config.Config
	txClient  *lighterclient.TxClient
}

// NewOrderPlacementClient creates a new order placement client
func NewOrderPlacementClient(apiClient *api.Client, cfg config.Config) (*OrderPlacementClient, error) {
	if cfg.APIKeyPrivateKey == "" {
		return nil, fmt.Errorf("LIGHTER_API_KEY_PRIVATE_KEY is required")
	}

	if cfg.AccountIndex == 0 {
		return nil, fmt.Errorf("account_index is required (set LIGHTER_ACCOUNT_INDEX)")
	}

	// Create SDK TxClient for transaction building. Nonce lookups share the API client's proxy pool.
	httpClient := apiClient.GetSDKClient()
	txClient, err := lighterclient.NewTxClient(httpClient, cfg.APIKeyPrivateKey, int64(cfg.AccountIndex), uint8(cfg.APIKeyIndex), cfg.ChainID)
	if err != nil {
		return nil, fmt.Errorf("failed to create TxClient: %w", err)
	}

	return &OrderPlacementClient{
		apiClient: apiClient,
		cfg:       cfg,
		txClient:  txClient,
	}, nil
}

// PlaceOrder places a new order
// marketSymbol: Market symbol (e.g., "ETH", "BTC")
// side: "buy" or "sell"
// orderType: "limit", "market", "stop-loss", "stop-loss-limit", "take-profit", "take-profit-limit", or "twap"
// price: Price in USDC (for limit/stop-loss-limit/take-profit-limit orders, execution price for stop-loss/take-profit)
// size: Size in tokens (e.g., 1.0 = 1 token)
// clientOrderIndex: Unique client order index (0 = auto-generate)
// timeInForce: "good-till-time", "immediate-or-cancel", or "post-only"
// orderExpiry: Expiration time in milliseconds (0 = default 1 hour)
// triggerPrice: Trigger price in USDC (for stop-loss/take-profit orders, 0 = use price)
func (c *OrderPlacementClient) PlaceOrder(ctx context.Context, marketSymbol string, side OrderSide, orderType OrderType, price float64, size float64, clientOrderIndex int64, timeInForce TimeInForce, orderExpiry int64, triggerPrice float64, authToken string) (string, error) {
	// Resolve market symbol to market ID
	marketID, err := markets.GetMarketIDGlobal(marketSymbol)
	if err != nil {
		return "", fmt.Errorf("failed to resolve market ID: %w", err)
	}

	// Convert market_id (uint32) to market_index (uint8)
	if marketID > 255 {
		return "", fmt.Errorf("market_id %d exceeds uint8 range (0-255)", marketID)
	}
	marketIndex := uint8(marketID)

	// Convert side to IsAsk (0 = buy, 1 = sell)
	var isAsk uint8
	if side == OrderSideSell {
		isAsk = 1
	} else {
		isAsk = 0
	}

	// Convert order type to SDK type
	var orderTypeSDK uint8
	switch orderType {
	case OrderTypeLimit:
		orderTypeSDK = lightertxtypes.LimitOrder
	case OrderTypeMarket:
		orderTypeSDK = lightertxtypes.MarketOrder
	case OrderTypeStopLoss:
		orderTypeSDK = lightertxtypes.StopLossOrder
	case OrderTypeStopLossLimit:
		orderTypeSDK = lightertxtypes.StopLossLimitOrder
	case OrderTypeTakeProfit:
		orderTypeSDK = lightertxtypes.TakeProfitOrder
	case OrderTypeTakeProfitLimit:
		orderTypeSDK = lightertxtypes.TakeProfitLimitOrder
	case OrderTypeTWAP:
		orderTypeSDK = lightertxtypes.TWAPOrder
	default:
		return "", fmt.Errorf("unsupported order type: %s (supported: limit, market, stop-loss, stop-loss-limit, take-profit, take-profit-limit, twap)", orderType)
	}

	// Convert time in force to SDK type
	// Note: Stop-loss and take-profit (non-limit) orders may not support all time-in-force options
	var tifSDK uint8
	switch timeInForce {
	case TIFGTT:
		tifSDK = lightertxtypes.GoodTillTime
	case TIFIOC:
		tifSDK = lightertxtypes.ImmediateOrCancel
	case TIFPO:
		tifSDK = lightertxtypes.PostOnly
	default:
		return "", fmt.Errorf("unsupported time in force: %s", timeInForce)
	}

	// For stop-loss, take-profit (non-limit), and market orders, force ImmediateOrCancel
	// These order types may not support GoodTillTime or PostOnly
	if orderType == OrderTypeStopLoss || orderType == OrderTypeTakeProfit || orderType == OrderTypeMarket {
		tifSDK = lightertxtypes.ImmediateOrCancel
	}

	// Convert size from tokens to the format expected by CreateOrderTxReq
	// Based on API order data: base_size = size * 10000 (ten-thousandths)
	// Example: size 1.0 -> base_size 10000, size 0.10 -> base_size 1000
	sizeTenThousandths := int64(size * 10_000)

	// Convert price from USDC to the format expected by CreateOrderTxReq
	// Based on API order data: base_price = price * 100 (hundredths of USDC)
	// Example: price "3001.00" -> base_price 300100
	// For market orders, price must still be set (use a high value for buy, low for sell)
	var priceHundredths uint32
	if orderType == OrderTypeMarket {
		// Market orders need a price - use a high value for buy orders, low for sell orders
		// This is a placeholder; the order will execute at market price
		if side == OrderSideBuy {
			priceHundredths = 99999999 // Very high price for buy market orders
		} else {
			priceHundredths = 1 // Very low price for sell market orders
		}
	} else {
		priceHundredths = uint32(price * 100)
	}

	// Convert trigger price (for stop-loss/take-profit orders)
	// If triggerPrice is 0, use price as trigger price
	var triggerPriceHundredths uint32
	if triggerPrice > 0 {
		triggerPriceHundredths = uint32(triggerPrice * 100)
	} else if orderType == OrderTypeStopLoss || orderType == OrderTypeStopLossLimit || orderType == OrderTypeTakeProfit || orderType == OrderTypeTakeProfitLimit {
		// For stop-loss/take-profit orders, if no trigger price specified, use price
		triggerPriceHundredths = priceHundredths
	}

	// Auto-generate client_order_index if 0
	if clientOrderIndex == 0 {
		clientOrderIndex = time.Now().UnixNano() / int64(time.Millisecond) // Use milliseconds timestamp
	}

	// Set default expiry (1 hour from now) if not provided
	if orderExpiry == 0 {
		orderExpiry = time.Now().Add(1 * time.Hour).UnixMilli()
	}

	// For ImmediateOrCancel orders:
	// - Market orders: OrderExpiry must be 0 (no expiry)
	// - Limit orders with IOC: OrderExpiry must be 0 (no expiry) - same as market orders
	// - Stop-loss/take-profit orders: Keep the default expiry (they use IOC but need expiry)
	if tifSDK == lightertxtypes.ImmediateOrCancel {
		if orderType == OrderTypeMarket || orderType == OrderTypeLimit {
			orderExpiry = 0
		}
		// Stop-loss and take-profit orders keep their expiry
	}

	// Get next nonce FIRST using REST API with auth token
	nonce, err := c.apiClient.NextNonceWithAuth(ctx, authToken)
	if err != nil {
		return "", fmt.Errorf("failed to get nonce: %w", err)
	}
	nonceInt64 := int64(nonce)
	fmt.Fprintf(os.Stderr, "[DEBUG] Got nonce from REST API: %d\n", nonceInt64)

	if nonce == 0 {
		return "", fmt.Errorf("invalid nonce: 0")
	}

	// Create transaction request
	txReq := &lightertypes.CreateOrderTxReq{
		MarketIndex:      marketIndex,
		ClientOrderIndex: clientOrderIndex,
		BaseAmount:       sizeTenThousandths,
		Price:            priceHundredths,
		IsAsk:            isAsk,
		Type:             orderTypeSDK,
		TimeInForce:      tifSDK,
		ReduceOnly:       0,                      // Not reduce-only
		TriggerPrice:     triggerPriceHundredths, // Trigger price for stop-loss/take-profit orders
		OrderExpiry:      orderExpiry,
	}

	// Create transaction options WITH THE CORRECT NONCE
	expiredAt := time.Now().Add(1 * time.Hour).UnixMilli()
	accountIndexForOps := int64(c.cfg.AccountIndex)
	apiKeyIndexForOps := uint8(c.cfg.APIKeyIndex)
	nonceInt64ForOps := nonceInt64

	ops := &lightertypes.TransactOpts{
		FromAccountIndex: &accountIndexForOps,
		ApiKeyIndex:      &apiKeyIndexForOps,
		ExpiredAt:        expiredAt,
		Nonce:            &nonceInt64ForOps,
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Creating order transaction with nonce=%d\n", nonceInt64ForOps)

	// Call FullFillDefaultOps to ensure the SDK uses our nonce
	filledOps, err := c.txClient.FullFillDefaultOps(ops)
	if err != nil {
		return "", fmt.Errorf("failed to fill default ops: %w", err)
	}

	// Ensure the nonce matches what we fetched
	if filledOps.Nonce != nil && *filledOps.Nonce != nonceInt64ForOps {
		fmt.Fprintf(os.Stderr, "[DEBUG] FullFillDefaultOps changed nonce from %d to %d, overriding...\n", nonceInt64ForOps, *filledOps.Nonce)
		filledOps.Nonce = &nonceInt64ForOps
	}

	// Build transaction
	orderTx, err := c.txClient.GetCreateOrderTransaction(txReq, filledOps)
	if err != nil {
		return "", fmt.Errorf("failed to build transaction: %w", err)
	}

	// Re-sign the transaction with the correct nonce
	if orderTx.Nonce != nonceInt64ForOps {
		fmt.Fprintf(os.Stderr, "[DEBUG] Nonce mismatch! SDK used %d, we want %d. Re-signing...\n", orderTx.Nonce, nonceInt64ForOps)
		orderTx.Nonce = nonceInt64ForOps
	}

	// Hash and sign the transaction
	msgHash, err := orderTx.Hash(c.cfg.ChainID)
	if err != nil {
		return "", fmt.Errorf("failed to hash transaction: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Transaction hash (for signing): %x\n", msgHash)

	keyManager := c.txClient.GetKeyManager()
	sig, err := keyManager.Sign(msgHash, sha256.New())
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Generated signature: %x (length: %d)\n", sig, len(sig))

	orderTx.Sig = sig
	fmt.Fprintf(os.Stderr, "[DEBUG] Re-signed transaction with nonce=%d\n", orderTx.Nonce)

	// Serialize and send transaction
	return c.sendOrderTransaction(ctx, orderTx, authToken)
}

// CancelOrder cancels a single order by client_order_index
// marketSymbol: Market symbol (e.g., "ETH", "BTC") - required to identify the market
// clientOrderIndex: Client order index to cancel
func (c *OrderPlacementClient) CancelOrder(ctx context.Context, marketSymbol string, clientOrderIndex int64, authToken string) (string, error) {
	// Resolve market symbol to market ID
	marketID, err := markets.GetMarketIDGlobal(marketSymbol)
	if err != nil {
		return "", fmt.Errorf("failed to resolve market ID: %w", err)
	}

	// Convert market_id (uint32) to market_index (uint8)
	if marketID > 255 {
		return "", fmt.Errorf("market_id %d exceeds uint8 range (0-255)", marketID)
	}
	marketIndex := uint8(marketID)

	// Get next nonce FIRST using REST API with auth token
	nonce, err := c.apiClient.NextNonceWithAuth(ctx, authToken)
	if err != nil {
		return "", fmt.Errorf("failed to get nonce: %w", err)
	}
	nonceInt64 := int64(nonce)
	fmt.Fprintf(os.Stderr, "[DEBUG] Got nonce from REST API: %d\n", nonceInt64)

	if nonce == 0 {
		return "", fmt.Errorf("invalid nonce: 0")
	}

	// Create transaction request
	// Note: CancelOrderTxReq uses MarketIndex and Index (not ClientOrderIndex)
	txReq := &lightertypes.CancelOrderTxReq{
		MarketIndex: marketIndex,
		Index:       clientOrderIndex,
	}

	// Create transaction options WITH THE CORRECT NONCE
	expiredAt := time.Now().Add(1 * time.Hour).UnixMilli()
	accountIndexForOps := int64(c.cfg.AccountIndex)
	apiKeyIndexForOps := uint8(c.cfg.APIKeyIndex)
	nonceInt64ForOps := nonceInt64

	ops := &lightertypes.TransactOpts{
		FromAccountIndex: &accountIndexForOps,
		ApiKeyIndex:      &apiKeyIndexForOps,
		ExpiredAt:        expiredAt,
		Nonce:            &nonceInt64ForOps,
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Creating cancel order transaction with nonce=%d\n", nonceInt64ForOps)

	// Call FullFillDefaultOps to ensure the SDK uses our nonce
	filledOps, err := c.txClient.FullFillDefaultOps(ops)
	if err != nil {
		return "", fmt.Errorf("failed to fill default ops: %w", err)
	}

	// Ensure the nonce matches what we fetched
	if filledOps.Nonce != nil && *filledOps.Nonce != nonceInt64ForOps {
		fmt.Fprintf(os.Stderr, "[DEBUG] FullFillDefaultOps changed nonce from %d to %d, overriding...\n", nonceInt64ForOps, *filledOps.Nonce)
		filledOps.Nonce = &nonceInt64ForOps
	}

	// Build transaction
	cancelTx, err := c.txClient.GetCancelOrderTransaction(txReq, filledOps)
	if err != nil {
		return "", fmt.Errorf("failed to build transaction: %w", err)
	}

	// Re-sign the transaction with the correct nonce
	if cancelTx.Nonce != nonceInt64ForOps {
		fmt.Fprintf(os.Stderr, "[DEBUG] Nonce mismatch! SDK used %d, we want %d. Re-signing...\n", cancelTx.Nonce, nonceInt64ForOps)
		cancelTx.Nonce = nonceInt64ForOps
	}

	// Hash and sign the transaction
	msgHash, err := cancelTx.Hash(c.cfg.ChainID)
	if err != nil {
		return "", fmt.Errorf("failed to hash transaction: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Transaction hash (for signing): %x\n", msgHash)

	keyManager := c.txClient.GetKeyManager()
	sig, err := keyManager.Sign(msgHash, sha256.New())
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Generated signature: %x (length: %d)\n", sig, len(sig))

	cancelTx.Sig = sig
	fmt.Fprintf(os.Stderr, "[DEBUG] Re-signed transaction with nonce=%d\n", cancelTx.Nonce)

	// Serialize and send transaction
	return c.sendOrderTransaction(ctx, cancelTx, authToken)
}

// CancelAllOrdersForMarket cancels all orders for a specific market
// This fetches all orders for the market and cancels them individually
func (c *OrderPlacementClient) CancelAllOrdersForMarket(ctx context.Context, marketSymbol string, authToken string) ([]string, error) {
	// Resolve market symbol to market ID
	marketID, err := markets.GetMarketIDGlobal(marketSymbol)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve market ID: %w", err)
	}

	// Convert market_id (uint32) to market_index (uint8)
	if marketID > 255 {
		return nil, fmt.Errorf("market_id %d exceeds uint8 range (0-255)", marketID)
	}
	marketIndex := uint8(marketID)

	// Fetch all orders for this account
	ordersClient := NewOrdersClient(c.apiClient, c.cfg)
	accountIndex := c.cfg.AccountIndex
	allOrders, err := ordersClient.GetOrders(ctx, &accountIndex, authToken)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch orders: %w", err)
	}

	// Filter orders for this market
	var marketOrders []*Order
	for _, order := range allOrders {
		if order.MarketIndex == marketIndex {
			marketOrders = append(marketOrders, order)
		}
	}

	if len(marketOrders) == 0 {
		fmt.Fprintf(os.Stderr, "[DEBUG] No orders found for market %s\n", marketSymbol)
		return []string{}, nil
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Found %d orders for market %s, cancelling each...\n", len(marketOrders), marketSymbol)

	// Cancel each order individually
	var txHashes []string
	var errors []error
	for _, order := range marketOrders {
		clientOrderIndex := int64(order.ClientOrderIndex)
		fmt.Fprintf(os.Stderr, "[DEBUG] Cancelling order: client_order_index=%d\n", clientOrderIndex)

		txHash, err := c.CancelOrder(ctx, marketSymbol, clientOrderIndex, authToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[DEBUG] Failed to cancel order %d: %v\n", clientOrderIndex, err)
			errors = append(errors, fmt.Errorf("order %d: %w", clientOrderIndex, err))
			continue
		}
		txHashes = append(txHashes, txHash)
	}

	if len(errors) > 0 {
		return txHashes, fmt.Errorf("failed to cancel %d/%d orders: %v", len(errors), len(marketOrders), errors[0])
	}

	return txHashes, nil
}

// CancelAllOrders cancels all open orders
// timeInForce: Time in force filter (0 = all, or use ImmediateCancelAll constant)
func (c *OrderPlacementClient) CancelAllOrders(ctx context.Context, authToken string) (string, error) {
	// Get next nonce FIRST using REST API with auth token
	nonce, err := c.apiClient.NextNonceWithAuth(ctx, authToken)
	if err != nil {
		return "", fmt.Errorf("failed to get nonce: %w", err)
	}
	nonceInt64 := int64(nonce)
	fmt.Fprintf(os.Stderr, "[DEBUG] Got nonce from REST API: %d\n", nonceInt64)

	if nonce == 0 {
		return "", fmt.Errorf("invalid nonce: 0")
	}

	// Create transaction request
	// CancelAllOrdersTxReq - check SDK docs for correct structure
	// Based on error "CancelAllTime should be nil", it seems Time should be nil
	txReq := &lightertypes.CancelAllOrdersTxReq{
		TimeInForce: lightertxtypes.ImmediateCancelAll, // Cancel all immediately
		Time:        0,                                 // Set to 0 or check if nil is needed
	}

	// Create transaction options WITH THE CORRECT NONCE
	expiredAt := time.Now().Add(1 * time.Hour).UnixMilli()
	accountIndexForOps := int64(c.cfg.AccountIndex)
	apiKeyIndexForOps := uint8(c.cfg.APIKeyIndex)
	nonceInt64ForOps := nonceInt64

	ops := &lightertypes.TransactOpts{
		FromAccountIndex: &accountIndexForOps,
		ApiKeyIndex:      &apiKeyIndexForOps,
		ExpiredAt:        expiredAt,
		Nonce:            &nonceInt64ForOps,
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Creating cancel all orders transaction with nonce=%d\n", nonceInt64ForOps)

	// Call FullFillDefaultOps to ensure the SDK uses our nonce
	filledOps, err := c.txClient.FullFillDefaultOps(ops)
	if err != nil {
		return "", fmt.Errorf("failed to fill default ops: %w", err)
	}

	// Ensure the nonce matches what we fetched
	if filledOps.Nonce != nil && *filledOps.Nonce != nonceInt64ForOps {
		fmt.Fprintf(os.Stderr, "[DEBUG] FullFillDefaultOps changed nonce from %d to %d, overriding...\n", nonceInt64ForOps, *filledOps.Nonce)
		filledOps.Nonce = &nonceInt64ForOps
	}

	// Build transaction
	cancelAllTx, err := c.txClient.GetCancelAllOrdersTransaction(txReq, filledOps)
	if err != nil {
		return "", fmt.Errorf("failed to build transaction: %w", err)
	}

	// Re-sign the transaction with the correct nonce
	if cancelAllTx.Nonce != nonceInt64ForOps {
		fmt.Fprintf(os.Stderr, "[DEBUG] Nonce mismatch! SDK used %d, we want %d. Re-signing...\n", cancelAllTx.Nonce, nonceInt64ForOps)
		cancelAllTx.Nonce = nonceInt64ForOps
	}

	// Hash and sign the transaction
	msgHash, err := cancelAllTx.Hash(c.cfg.ChainID)
	if err != nil {
		return "", fmt.Errorf("failed to hash transaction: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Transaction hash (for signing): %x\n", msgHash)

	keyManager := c.txClient.GetKeyManager()
	sig, err := keyManager.Sign(msgHash, sha256.New())
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Generated signature: %x (length: %d)\n", sig, len(sig))

	cancelAllTx.Sig = sig
	fmt.Fprintf(os.Stderr, "[DEBUG] Re-signed transaction with nonce=%d\n", cancelAllTx.Nonce)

	// Serialize and send transaction
	return c.sendOrderTransaction(ctx, cancelAllTx, authToken)
}

// sendOrderTransaction serializes and sends an order transaction
func (c *OrderPlacementClient) sendOrderTransaction(ctx context.Context, txInfo lightertxtypes.TxInfo, authToken string) (string, error) {
	// Get transaction info as JSON string
	txInfoJSON, err := txInfo.GetTxInfo()
	if err != nil {
		return "", fmt.Errorf("failed to serialize transaction: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] GetTxInfo() returned: %s\n", txInfoJSON)

	// Parse to check if Sig is already in the correct format
	var txPayload map[string]interface{}
	if err := json.Unmarshal([]byte(txInfoJSON), &txPayload); err != nil {
		return "", fmt.Errorf("failed to parse transaction JSON: %w", err)
	}

	// Extract signature and convert to base64 if needed
	var sig []byte
	switch tx := txInfo.(type) {
	case *lightertxtypes.L2CreateOrderTxInfo:
		sig = tx.Sig
	case *lightertxtypes.L2CancelOrderTxInfo:
		sig = tx.Sig
	case *lightertxtypes.L2CancelAllOrdersTxInfo:
		sig = tx.Sig
	default:
		return "", fmt.Errorf("unsupported transaction type")
	}

	// Convert to base64
	sigBase64 := base64.StdEncoding.EncodeToString(sig)
	txPayload["Sig"] = sigBase64
	fmt.Fprintf(os.Stderr, "[DEBUG] Added signature as base64: %s\n", sigBase64)

	// Get tx_type from the transaction info (numeric value, like margin.go does)
	var txType uint8
	switch tx := txInfo.(type) {
	case *lightertxtypes.L2CreateOrderTxInfo:
		txType = tx.GetTxType() // Returns numeric tx type (e.g., 18)
	case *lightertxtypes.L2CancelOrderTxInfo:
		txType = tx.GetTxType() // Returns numeric tx type (e.g., 19)
	case *lightertxtypes.L2CancelAllOrdersTxInfo:
		txType = tx.GetTxType() // Returns numeric tx type (e.g., 20)
	default:
		return "", fmt.Errorf("unsupported transaction type")
	}

	// Debug: log the nonce being used
	if nonceVal, ok := txPayload["Nonce"]; ok {
		fmt.Fprintf(os.Stderr, "[DEBUG] Nonce in tx_info: %v\n", nonceVal)
	}

	// Re-marshal tx_info as a JSON string (this is what the API expects!)
	txInfoBytes, err := json.Marshal(txPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tx_info: %w", err)
	}
	txInfoString := string(txInfoBytes)

	// Build the final payload matching the frontend format (same as margin.go):
	// {
	//   "tx_type": "18",  // String representation of numeric tx type!
	//   "tx_info": "{...}",  // JSON string!
	//   "price_protection": "false"
	// }
	payload := map[string]interface{}{
		"tx_type":          fmt.Sprintf("%d", txType), // String, not number!
		"tx_info":          txInfoString,              // JSON string, not object!
		"price_protection": "false",
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] Final transaction payload: tx_type=%d (as string: %q), tx_info length: %d\n", txType, fmt.Sprintf("%d", txType), len(txInfoString))

	// Send transaction
	return c.apiClient.SendTxRaw(ctx, payload, authToken)
}
