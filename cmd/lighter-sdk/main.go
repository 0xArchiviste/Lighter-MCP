// Command lighter-sdk is the Lighter.xyz SDK CLI. It uses the same environment
// variables and logic as lighter-mcp (pkg/lighterapp.DispatchTool). For network
// access to the same API surface, run: lighter-mcp -transport=grpc
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/lighterapp"
	"github.com/0xarchiviste/lighter-mcp/pkg/lightergrpc"
	"github.com/0xarchiviste/lighter-mcp/pkg/walletstore"
	"github.com/spf13/cobra"
)

var app *lighterapp.App

func ensureApp() error {
	if app != nil {
		return nil
	}
	cfg := config.Load()
	var err error
	cfg, err = walletstore.Resolve(context.Background(), cfg)
	if err != nil {
		return err
	}
	if cfg.APIKeyPrivateKey == "" {
		return fmt.Errorf("LIGHTER_API_KEY_PRIVATE_KEY is required (or use LIGHTER_WALLET_BACKEND=supabase with wallet env)")
	}
	if cfg.AccountIndex == 0 {
		return fmt.Errorf("LIGHTER_ACCOUNT_INDEX must be non-zero (or select a supabase wallet row)")
	}
	app, err = lighterapp.NewApp(cfg)
	return err
}

func main() {
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func mustJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Fatal(err)
	}
}

func runTool(ctx context.Context, name string, args map[string]any) error {
	if err := ensureApp(); err != nil {
		return err
	}
	text, isErr, err := app.DispatchTool(ctx, name, args)
	if err != nil {
		return err
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		// Not valid JSON (should be rare); print raw text.
		fmt.Fprintln(os.Stdout, text)
	} else {
		mustJSON(parsed)
	}
	if isErr {
		os.Exit(2)
	}
	return nil
}

var rootCmd = &cobra.Command{
	Use:   "lighter-sdk",
	Short: "Lighter.xyz SDK CLI (same config as lighter-mcp)",
	Long: `Commands call Lighter with LIGHTER_* credentials from the environment (and optional .env).

Multi-wallet (Supabase): set LIGHTER_WALLET_BACKEND=supabase, LIGHTER_WALLET_ID or LIGHTER_WALLET_NAME,
LIGHTER_WALLET_MASTER_PASSWORD, LIGHTER_WALLET_UNLOCK_PASSWORD, and LIGHTER_SUPABASE_URL + LIGHTER_SUPABASE_SERVICE_KEY
(or SUPABASE_URL + SUPABASE_SERVICE_ROLE_KEY). Use "lighter-sdk wallet" to add/list/delete encrypted rows.

For the same operations over gRPC (lighter.v1.Lighter + gRPC-MCP), run:
  lighter-mcp -transport=grpc -grpc-addr=127.0.0.1:9090`,
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().SortFlags = false

	rootCmd.AddCommand(&cobra.Command{
		Use:   "balance",
		Short: "Account balance and collateral (lighter_get_balance)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTool(cmd.Context(), "lighter_get_balance", nil)
		},
	})

	marketsCmd := &cobra.Command{
		Use:   "markets",
		Short: "List markets with prices (lighter_list_markets)",
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			return runTool(cmd.Context(), "lighter_list_markets", map[string]any{"limit": limit})
		},
	}
	marketsCmd.Flags().Int("limit", 10, "max markets to return")
	rootCmd.AddCommand(marketsCmd)

	mktCmd := &cobra.Command{
		Use:   "market",
		Short: "One market by symbol (lighter_get_market)",
		RunE: func(cmd *cobra.Command, args []string) error {
			sym, _ := cmd.Flags().GetString("symbol")
			if sym == "" {
				return fmt.Errorf("--symbol is required")
			}
			return runTool(cmd.Context(), "lighter_get_market", map[string]any{"symbol": sym})
		},
	}
	mktCmd.Flags().String("symbol", "", "market symbol e.g. ETH")
	_ = mktCmd.MarkFlagRequired("symbol")
	rootCmd.AddCommand(mktCmd)

	posCmd := &cobra.Command{
		Use:   "positions",
		Short: "List positions (lighter_list_positions)",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, _ := cmd.Flags().GetString("market")
			return runTool(cmd.Context(), "lighter_list_positions", map[string]any{"market": m})
		},
	}
	posCmd.Flags().String("market", "", "optional filter by market symbol")
	rootCmd.AddCommand(posCmd)

	placeCmd := &cobra.Command{
		Use:   "place-limit",
		Short: "Place GTT limit order (lighter_place_limit_order)",
		RunE: func(cmd *cobra.Command, args []string) error {
			market, _ := cmd.Flags().GetString("market")
			side, _ := cmd.Flags().GetString("side")
			price, _ := cmd.Flags().GetFloat64("price")
			size, _ := cmd.Flags().GetFloat64("size")
			return runTool(cmd.Context(), "lighter_place_limit_order", map[string]any{
				"market": market, "side": side, "price": price, "size": size,
			})
		},
	}
	placeCmd.Flags().String("market", "", "")
	placeCmd.Flags().String("side", "", "buy or sell")
	placeCmd.Flags().Float64("price", 0, "")
	placeCmd.Flags().Float64("size", 0, "")
	for _, f := range []string{"market", "side", "price", "size"} {
		_ = placeCmd.MarkFlagRequired(f)
	}
	rootCmd.AddCommand(placeCmd)

	cancelCmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel order by client index (lighter_cancel_order)",
		RunE: func(cmd *cobra.Command, args []string) error {
			market, _ := cmd.Flags().GetString("market")
			coi, _ := cmd.Flags().GetInt64("client-order-index")
			return runTool(cmd.Context(), "lighter_cancel_order", map[string]any{
				"market": market, "client_order_index": coi,
			})
		},
	}
	cancelCmd.Flags().String("market", "", "")
	cancelCmd.Flags().Int64("client-order-index", 0, "")
	_ = cancelCmd.MarkFlagRequired("market")
	_ = cancelCmd.MarkFlagRequired("client-order-index")
	rootCmd.AddCommand(cancelCmd)

	tpslCmd := &cobra.Command{
		Use:   "tpsl",
		Short: "Set TP/SL (lighter_set_tp_sl)",
		RunE: func(cmd *cobra.Command, args []string) error {
			market, _ := cmd.Flags().GetString("market")
			tp, _ := cmd.Flags().GetFloat64("tp-price")
			sl, _ := cmd.Flags().GetFloat64("sl-price")
			return runTool(cmd.Context(), "lighter_set_tp_sl", map[string]any{
				"market": market, "tp_price": tp, "sl_price": sl,
			})
		},
	}
	tpslCmd.Flags().String("market", "", "")
	tpslCmd.Flags().Float64("tp-price", 0, "")
	tpslCmd.Flags().Float64("sl-price", 0, "")
	_ = tpslCmd.MarkFlagRequired("market")
	rootCmd.AddCommand(tpslCmd)

	closeCmd := &cobra.Command{
		Use:   "close",
		Short: "Close position (lighter_close_position)",
		RunE: func(cmd *cobra.Command, args []string) error {
			market, _ := cmd.Flags().GetString("market")
			return runTool(cmd.Context(), "lighter_close_position", map[string]any{"market": market})
		},
	}
	closeCmd.Flags().String("market", "", "")
	_ = closeCmd.MarkFlagRequired("market")
	rootCmd.AddCommand(closeCmd)

	indCmd := &cobra.Command{
		Use:   "indicator",
		Short: "Technical indicator (lighter_calculate_indicator)",
		RunE: func(cmd *cobra.Command, args []string) error {
			market, _ := cmd.Flags().GetString("market")
			ind, _ := cmd.Flags().GetString("name")
			period, _ := cmd.Flags().GetInt("period")
			return runTool(cmd.Context(), "lighter_calculate_indicator", map[string]any{
				"market": market, "indicator": ind, "period": period,
			})
		},
	}
	indCmd.Flags().String("market", "", "")
	indCmd.Flags().String("name", "", "rsi|macd|sma|ema|bollinger|atr|all")
	indCmd.Flags().Int("period", 14, "")
	_ = indCmd.MarkFlagRequired("market")
	_ = indCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(indCmd)

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Run gRPC-MCP + lighter.v1.Lighter SDK server (same as lighter-mcp -transport=grpc)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureApp(); err != nil {
				return err
			}
			addr, _ := cmd.Flags().GetString("addr")
			auth, _ := cmd.Flags().GetString("auth")
			key, _ := cmd.Flags().GetString("api-key")
			return lightergrpc.Serve(app, addr, auth, key)
		},
	}
	serveCmd.Flags().String("addr", "127.0.0.1:9090", "listen address")
	serveCmd.Flags().String("auth", "none", "none|bearer|apikey")
	serveCmd.Flags().String("api-key", "dev-key", "expected x-mcp-api-key when --auth=apikey")
	rootCmd.AddCommand(serveCmd)
}
