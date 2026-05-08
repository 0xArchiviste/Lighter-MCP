package lighterapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/0xarchiviste/lighter-mcp/pkg/account"
	"github.com/0xarchiviste/lighter-mcp/pkg/markets"
)

// DispatchTool runs one MCP tool by name. args comes from JSON/tool arguments (e.g. Struct.AsMap()).
// Returns JSON text suitable for MCP text content, a flag if the outcome is an application-level error, and transport errors.
func (a *App) DispatchTool(ctx context.Context, name string, args map[string]any) (jsonText string, isError bool, err error) {
	tok, err := a.AuthToken(ctx)
	if err != nil {
		return "", false, fmt.Errorf("auth: %w", err)
	}

	switch name {
	case "lighter_get_balance":
		idx := a.Cfg.AccountIndex
		data, err := account.NewClient(a.APIClient).GetBalance(ctx, &idx, tok)
		if err != nil {
			return errText("get balance: %v", err), true, nil
		}
		return mustJSON(data), false, nil

	case "lighter_list_markets":
		limit := intFromMap(args, "limit", 10)
		if limit <= 0 {
			limit = 10
		}
		list, err := markets.NewClient(a.APIClient).GetMarketsWithPrices(ctx, tok, limit)
		if err != nil {
			return errText("list markets: %v", err), true, nil
		}
		return mustJSON(list), false, nil

	case "lighter_get_market":
		sym := stringFromMap(args, "symbol")
		if sym == "" {
			return errText("symbol is required"), true, nil
		}
		mkt, err := markets.NewClient(a.APIClient).GetMarketWithPrices(ctx, sym, tok)
		if err != nil {
			return errText("get market: %v", err), true, nil
		}
		return mustJSON(mkt), false, nil

	case "lighter_list_positions":
		mkt := stringFromMap(args, "market")
		pos, err := a.PositionsClient.ListPositions(ctx, mkt, tok)
		if err != nil {
			return errText("list positions: %v", err), true, nil
		}
		return mustJSON(pos), false, nil

	case "lighter_place_limit_order":
		market := stringFromMap(args, "market")
		sideStr := stringFromMap(args, "side")
		if market == "" || sideStr == "" {
			return errText("market and side are required"), true, nil
		}
		var side account.OrderSide
		switch sideStr {
		case "buy", "BUY":
			side = account.OrderSideBuy
		case "sell", "SELL":
			side = account.OrderSideSell
		default:
			return errText("side must be buy or sell"), true, nil
		}
		price := floatFromMap(args, "price")
		size := floatFromMap(args, "size")
		tx, err := a.OrderClient.PlaceOrder(ctx, market, side, account.OrderTypeLimit, price, size, 0, account.TIFGTT, 0, 0, tok)
		if err != nil {
			return errText("place order: %v", err), true, nil
		}
		return mustJSON(map[string]string{"tx_hash": tx}), false, nil

	case "lighter_cancel_order":
		market := stringFromMap(args, "market")
		if market == "" {
			return errText("market is required"), true, nil
		}
		coi := int64FromMap(args, "client_order_index")
		tx, err := a.OrderClient.CancelOrder(ctx, market, coi, tok)
		if err != nil {
			return errText("cancel order: %v", err), true, nil
		}
		return mustJSON(map[string]string{"tx_hash": tx}), false, nil

	case "lighter_set_tp_sl":
		market := stringFromMap(args, "market")
		if market == "" {
			return errText("market is required"), true, nil
		}
		tp := floatFromMap(args, "tp_price")
		sl := floatFromMap(args, "sl_price")
		hashes, err := a.TPSLClient.SetTPSL(ctx, market, tp, sl, tok)
		if err != nil {
			return errText("set tp/sl: %v", err), true, nil
		}
		out := map[string]string{}
		if len(hashes) > 0 {
			out["tp_tx_hash"] = hashes[0]
		}
		if len(hashes) > 1 {
			out["sl_tx_hash"] = hashes[1]
		}
		return mustJSON(out), false, nil

	case "lighter_close_position":
		market := stringFromMap(args, "market")
		if market == "" {
			return errText("market is required"), true, nil
		}
		tx, err := a.PositionsClient.ClosePosition(ctx, market, tok)
		if err != nil {
			return errText("close position: %v", err), true, nil
		}
		return mustJSON(map[string]string{"tx_hash": tx}), false, nil

	case "lighter_calculate_indicator":
		market := stringFromMap(args, "market")
		ind := stringFromMap(args, "indicator")
		if market == "" || ind == "" {
			return errText("market and indicator are required"), true, nil
		}
		period := intFromMap(args, "period", 14)
		if period <= 0 {
			period = 14
		}
		var result any
		switch ind {
		case "rsi":
			result, err = a.IndicatorService.CalculateRSI(ctx, market, period, tok)
		case "macd":
			result, err = a.IndicatorService.CalculateMACD(ctx, market, tok)
		case "sma":
			result, err = a.IndicatorService.CalculateMovingAverage(ctx, market, period, tok)
		case "ema":
			result, err = a.IndicatorService.CalculateEMA(ctx, market, period, tok)
		case "bollinger":
			result, err = a.IndicatorService.CalculateBollingerBands(ctx, market, period, tok)
		case "atr":
			result, err = a.IndicatorService.CalculateATR(ctx, market, period, tok)
		case "all":
			result, err = a.IndicatorService.CalculateAllIndicators(ctx, market, tok)
		default:
			return errText("unknown indicator: %s", ind), true, nil
		}
		if err != nil {
			return errText("indicator: %v", err), true, nil
		}
		return mustJSON(result), false, nil

	default:
		return errText("unknown tool: %s", name), true, nil
	}
}

func errText(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error":"json encode: %v"}`, err)
	}
	return string(b)
}

func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func floatFromMap(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		f, _ := strconv.ParseFloat(fmt.Sprint(t), 64)
		return f
	}
}

func intFromMap(m map[string]any, key string, def int) int {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		i, _ := strconv.Atoi(t)
		return i
	default:
		i, _ := strconv.Atoi(fmt.Sprint(t))
		return i
	}
}

func int64FromMap(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case json.Number:
		i, _ := t.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(t, 10, 64)
		return i
	default:
		i, _ := strconv.ParseInt(fmt.Sprint(t), 10, 64)
		return i
	}
}
