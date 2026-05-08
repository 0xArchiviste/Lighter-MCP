package lighterapp

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func structToMap(v any) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return map[string]any{}, nil
	}
	return m, nil
}

func wrapDispatch(ctx context.Context, app *App, name string, args any) (*mcp.CallToolResult, any, error) {
	m, err := structToMap(args)
	if err != nil {
		return nil, nil, err
	}
	text, isErr, err := app.DispatchTool(ctx, name, m)
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: isErr,
	}, nil, nil
}

// RegisterStdioTools registers all Lighter tools on an MCP stdio server.
func RegisterStdioTools(server *mcp.Server, app *App) {
	type empty struct{}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_get_balance",
		Description: "Fetch collateral, balances, and high-level account info for the configured Lighter account index.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		return wrapDispatch(ctx, app, "lighter_get_balance", struct{}{})
	})

	type listMarketsArgs struct {
		Limit int `json:"limit,omitempty" jsonschema:"maximum number of markets with prices to return (default 10)"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_list_markets",
		Description: "List markets with recent price data from Lighter.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listMarketsArgs) (*mcp.CallToolResult, any, error) {
		return wrapDispatch(ctx, app, "lighter_list_markets", in)
	})

	type getMarketArgs struct {
		Symbol string `json:"symbol" jsonschema:"market symbol e.g. ETH or BTC"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_get_market",
		Description: "Get one market by symbol including price fields.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getMarketArgs) (*mcp.CallToolResult, any, error) {
		return wrapDispatch(ctx, app, "lighter_get_market", in)
	})

	type listPositionsArgs struct {
		Market string `json:"market,omitempty" jsonschema:"optional market symbol to filter positions"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_list_positions",
		Description: "List open positions for the configured account, optionally filtered by market symbol.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listPositionsArgs) (*mcp.CallToolResult, any, error) {
		return wrapDispatch(ctx, app, "lighter_list_positions", in)
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
		return wrapDispatch(ctx, app, "lighter_place_limit_order", in)
	})

	type cancelOrderArgs struct {
		Market           string `json:"market" jsonschema:"market symbol"`
		ClientOrderIndex int64  `json:"client_order_index" jsonschema:"client order index to cancel"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_cancel_order",
		Description: "Cancel an open order by client order index.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in cancelOrderArgs) (*mcp.CallToolResult, any, error) {
		return wrapDispatch(ctx, app, "lighter_cancel_order", in)
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
		return wrapDispatch(ctx, app, "lighter_set_tp_sl", in)
	})

	type closeArgs struct {
		Market string `json:"market" jsonschema:"market symbol to close"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lighter_close_position",
		Description: "Close an open position on a market (sends reducing order flow via Lighter API).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in closeArgs) (*mcp.CallToolResult, any, error) {
		return wrapDispatch(ctx, app, "lighter_close_position", in)
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
		return wrapDispatch(ctx, app, "lighter_calculate_indicator", in)
	})
}