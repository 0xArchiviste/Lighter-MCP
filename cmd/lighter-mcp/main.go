// Command lighter-mcp is a Model Context Protocol server for Lighter.xyz (stdio transport).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/0xarchiviste/lighter-mcp/pkg/account"
	"github.com/0xarchiviste/lighter-mcp/pkg/api"
	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/indicators"
	"github.com/0xarchiviste/lighter-mcp/pkg/markets"
	"github.com/0xarchiviste/lighter-mcp/pkg/positions"
	"github.com/0xarchiviste/lighter-mcp/pkg/signer"
	tpsl "github.com/0xarchiviste/lighter-mcp/pkg/tp-sl"
)

// lighterApp wires Lighter API clients for MCP tool handlers.
type lighterApp struct {
	cfg              config.Config
	apiClient        *api.Client
	signer           *signer.Signer
	orderClient      *account.OrderPlacementClient
	positionsClient  *positions.PositionsClient
	tpslClient       *tpsl.TPSLClient
	indicatorService *indicators.IndicatorService
}

func (a *lighterApp) authToken(ctx context.Context) (string, error) {
	return a.signer.CreateAuthTokenWithExpiry(ctx, 3600)
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}, nil, nil
}

func errResult(format string, args ...any) (*mcp.CallToolResult, any, error) {
	msg := fmt.Sprintf(format, args...)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}, nil, nil
}

func main() {
	cfg := config.Load()
	if cfg.APIKeyPrivateKey == "" {
		log.Fatal("LIGHTER_API_KEY_PRIVATE_KEY is required")
	}
	if cfg.AccountIndex == 0 {
		log.Fatal("LIGHTER_ACCOUNT_INDEX must be set to a non-zero account index")
	}

	apiClient := api.New(cfg)
	s, err := signer.New(signer.Config{
		BaseURL:          cfg.BaseURL,
		APIKeyPrivateKey: cfg.APIKeyPrivateKey,
		AccountIndex:     cfg.AccountIndex,
		APIKeyIndex:      cfg.APIKeyIndex,
	})
	if err != nil {
		log.Fatalf("signer: %v", err)
	}
	s.SetNonceProvider(apiClient)

	orderClient, err := account.NewOrderPlacementClient(apiClient, cfg)
	if err != nil {
		log.Fatalf("order client: %v", err)
	}
	positionsClient, err := positions.NewPositionsClient(apiClient, cfg)
	if err != nil {
		log.Fatalf("positions client: %v", err)
	}
	tpslClient, err := tpsl.NewTPSLClient(apiClient, cfg)
	if err != nil {
		log.Fatalf("tp/sl client: %v", err)
	}

	app := &lighterApp{
		cfg:              cfg,
		apiClient:        apiClient,
		signer:           s,
		orderClient:      orderClient,
		positionsClient:  positionsClient,
		tpslClient:       tpslClient,
		indicatorService: indicators.NewIndicatorService(apiClient),
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "lighter-mcp", Version: "0.1.0"}, nil)

	type empty struct{}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_get_balance",
		Description: "Fetch collateral, balances, and high-level account info for the configured Lighter account index.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		tok, err := app.authToken(ctx)
		if err != nil {
			return errResult("auth: %v", err)
		}
		idx := app.cfg.AccountIndex
		data, err := account.NewClient(app.apiClient).GetBalance(ctx, &idx, tok)
		if err != nil {
			return errResult("get balance: %v", err)
		}
		return jsonResult(data)
	})

	type listMarketsArgs struct {
		Limit int `json:"limit,omitempty" jsonschema:"maximum number of markets with prices to return (default 10)"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_list_markets",
		Description: "List markets with recent price data from Lighter.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listMarketsArgs) (*mcp.CallToolResult, any, error) {
		tok, err := app.authToken(ctx)
		if err != nil {
			return errResult("auth: %v", err)
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 10
		}
		list, err := markets.NewClient(app.apiClient).GetMarketsWithPrices(ctx, tok, limit)
		if err != nil {
			return errResult("list markets: %v", err)
		}
		return jsonResult(list)
	})

	type getMarketArgs struct {
		Symbol string `json:"symbol" jsonschema:"market symbol e.g. ETH or BTC"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_get_market",
		Description: "Get one market by symbol including price fields.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getMarketArgs) (*mcp.CallToolResult, any, error) {
		if in.Symbol == "" {
			return errResult("symbol is required")
		}
		tok, err := app.authToken(ctx)
		if err != nil {
			return errResult("auth: %v", err)
		}
		mkt, err := markets.NewClient(app.apiClient).GetMarketWithPrices(ctx, in.Symbol, tok)
		if err != nil {
			return errResult("get market: %v", err)
		}
		return jsonResult(mkt)
	})

	type listPositionsArgs struct {
		Market string `json:"market,omitempty" jsonschema:"optional market symbol to filter positions"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_list_positions",
		Description: "List open positions for the configured account, optionally filtered by market symbol.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listPositionsArgs) (*mcp.CallToolResult, any, error) {
		tok, err := app.authToken(ctx)
		if err != nil {
			return errResult("auth: %v", err)
		}
		pos, err := app.positionsClient.ListPositions(ctx, in.Market, tok)
		if err != nil {
			return errResult("list positions: %v", err)
		}
		return jsonResult(pos)
	})

	type placeOrderArgs struct {
		Market string  `json:"market" jsonschema:"market symbol e.g. ETH"`
		Side   string  `json:"side" jsonschema:"buy or sell"`
		Price  float64 `json:"price" jsonschema:"limit price"`
		Size   float64 `json:"size" jsonschema:"order size in base asset"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_place_limit_order",
		Description: "Place a good-till-time limit order on a market.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in placeOrderArgs) (*mcp.CallToolResult, any, error) {
		if in.Market == "" || in.Side == "" {
			return errResult("market and side are required")
		}
		var side account.OrderSide
		switch in.Side {
		case "buy", "BUY":
			side = account.OrderSideBuy
		case "sell", "SELL":
			side = account.OrderSideSell
		default:
			return errResult("side must be buy or sell")
		}
		tok, err := app.authToken(ctx)
		if err != nil {
			return errResult("auth: %v", err)
		}
		tx, err := app.orderClient.PlaceOrder(ctx, in.Market, side, account.OrderTypeLimit, in.Price, in.Size, 0, account.TIFGTT, 0, 0, tok)
		if err != nil {
			return errResult("place order: %v", err)
		}
		return jsonResult(map[string]string{"tx_hash": tx})
	})

	type cancelOrderArgs struct {
		Market           string `json:"market" jsonschema:"market symbol"`
		ClientOrderIndex int64  `json:"client_order_index" jsonschema:"client order index to cancel"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_cancel_order",
		Description: "Cancel an open order by client order index.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in cancelOrderArgs) (*mcp.CallToolResult, any, error) {
		if in.Market == "" {
			return errResult("market is required")
		}
		tok, err := app.authToken(ctx)
		if err != nil {
			return errResult("auth: %v", err)
		}
		tx, err := app.orderClient.CancelOrder(ctx, in.Market, in.ClientOrderIndex, tok)
		if err != nil {
			return errResult("cancel order: %v", err)
		}
		return jsonResult(map[string]string{"tx_hash": tx})
	})

	type tpslArgs struct {
		Market  string  `json:"market" jsonschema:"market symbol"`
		TpPrice float64 `json:"tp_price" jsonschema:"take profit price"`
		SlPrice float64 `json:"sl_price" jsonschema:"stop loss price"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_set_tp_sl",
		Description: "Set take-profit and stop-loss orders sized to the current position.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in tpslArgs) (*mcp.CallToolResult, any, error) {
		if in.Market == "" {
			return errResult("market is required")
		}
		tok, err := app.authToken(ctx)
		if err != nil {
			return errResult("auth: %v", err)
		}
		hashes, err := app.tpslClient.SetTPSL(ctx, in.Market, in.TpPrice, in.SlPrice, tok)
		if err != nil {
			return errResult("set tp/sl: %v", err)
		}
		out := map[string]string{}
		if len(hashes) > 0 {
			out["tp_tx_hash"] = hashes[0]
		}
		if len(hashes) > 1 {
			out["sl_tx_hash"] = hashes[1]
		}
		return jsonResult(out)
	})

	type closeArgs struct {
		Market string `json:"market" jsonschema:"market symbol to close"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_close_position",
		Description: "Close an open position on a market (sends reducing order flow via Lighter API).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in closeArgs) (*mcp.CallToolResult, any, error) {
		if in.Market == "" {
			return errResult("market is required")
		}
		tok, err := app.authToken(ctx)
		if err != nil {
			return errResult("auth: %v", err)
		}
		tx, err := app.positionsClient.ClosePosition(ctx, in.Market, tok)
		if err != nil {
			return errResult("close position: %v", err)
		}
		return jsonResult(map[string]string{"tx_hash": tx})
	})

	type indicatorArgs struct {
		Market    string `json:"market" jsonschema:"market symbol"`
		Indicator string `json:"indicator" jsonschema:"one of: rsi, macd, sma, ema, bollinger, atr, all"`
		Period    int    `json:"period,omitempty" jsonschema:"lookback period where applicable (default 14)"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_calculate_indicator",
		Description: "Compute technical indicators from recent Lighter candle history.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in indicatorArgs) (*mcp.CallToolResult, any, error) {
		if in.Market == "" || in.Indicator == "" {
			return errResult("market and indicator are required")
		}
		period := in.Period
		if period <= 0 {
			period = 14
		}
		tok, err := app.authToken(ctx)
		if err != nil {
			return errResult("auth: %v", err)
		}
		var result any
		switch in.Indicator {
		case "rsi":
			result, err = app.indicatorService.CalculateRSI(ctx, in.Market, period, tok)
		case "macd":
			result, err = app.indicatorService.CalculateMACD(ctx, in.Market, tok)
		case "sma":
			result, err = app.indicatorService.CalculateMovingAverage(ctx, in.Market, period, tok)
		case "ema":
			result, err = app.indicatorService.CalculateEMA(ctx, in.Market, period, tok)
		case "bollinger":
			result, err = app.indicatorService.CalculateBollingerBands(ctx, in.Market, period, tok)
		case "atr":
			result, err = app.indicatorService.CalculateATR(ctx, in.Market, period, tok)
		case "all":
			result, err = app.indicatorService.CalculateAllIndicators(ctx, in.Market, tok)
		default:
			return errResult("unknown indicator: %s", in.Indicator)
		}
		if err != nil {
			return errResult("indicator: %v", err)
		}
		return jsonResult(result)
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}
