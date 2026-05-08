// Package sdkgrpc implements the lighter.v1.Lighter programmatic API over gRPC.
package sdkgrpc

import (
	"context"

	lighv1 "github.com/0xarchiviste/lighter-mcp/gen/lighter/v1"
	"github.com/0xarchiviste/lighter-mcp/pkg/grpcmcp"
	"github.com/0xarchiviste/lighter-mcp/pkg/lighterapp"
	emptypb "google.golang.org/protobuf/types/known/emptypb"
	structpb "google.golang.org/protobuf/types/known/structpb"
)

// Server implements lighter.v1.Lighter by delegating to lighterapp.DispatchTool.
type Server struct {
	lighv1.UnimplementedLighterServer
	App *lighterapp.App
}

// NewServer constructs the SDK gRPC service.
func NewServer(app *lighterapp.App) *Server {
	return &Server{App: app}
}

func (s *Server) rpcValue(ctx context.Context, tool string, args map[string]any) (*structpb.Value, error) {
	txt, _, err := s.App.DispatchTool(ctx, tool, args)
	if err != nil {
		return nil, err
	}
	return grpcmcp.JSONToProtoValue(txt)
}

func (s *Server) GetBalance(ctx context.Context, _ *emptypb.Empty) (*structpb.Value, error) {
	return s.rpcValue(ctx, "lighter_get_balance", nil)
}

func (s *Server) ListMarkets(ctx context.Context, in *lighv1.ListMarketsRequest) (*structpb.Value, error) {
	return s.rpcValue(ctx, "lighter_list_markets", map[string]any{"limit": int(in.GetLimit())})
}

func (s *Server) GetMarket(ctx context.Context, in *lighv1.GetMarketRequest) (*structpb.Value, error) {
	return s.rpcValue(ctx, "lighter_get_market", map[string]any{"symbol": in.GetSymbol()})
}

func (s *Server) ListPositions(ctx context.Context, in *lighv1.ListPositionsRequest) (*structpb.Value, error) {
	return s.rpcValue(ctx, "lighter_list_positions", map[string]any{"market": in.GetMarket()})
}

func (s *Server) PlaceLimitOrder(ctx context.Context, in *lighv1.PlaceLimitOrderRequest) (*structpb.Value, error) {
	return s.rpcValue(ctx, "lighter_place_limit_order", map[string]any{
		"market": in.GetMarket(),
		"side":   in.GetSide(),
		"price":  in.GetPrice(),
		"size":   in.GetSize(),
	})
}

func (s *Server) CancelOrder(ctx context.Context, in *lighv1.CancelOrderRequest) (*structpb.Value, error) {
	return s.rpcValue(ctx, "lighter_cancel_order", map[string]any{
		"market":               in.GetMarket(),
		"client_order_index": in.GetClientOrderIndex(),
	})
}

func (s *Server) SetTPSL(ctx context.Context, in *lighv1.SetTPSLRequest) (*structpb.Value, error) {
	return s.rpcValue(ctx, "lighter_set_tp_sl", map[string]any{
		"market":   in.GetMarket(),
		"tp_price": in.GetTpPrice(),
		"sl_price": in.GetSlPrice(),
	})
}

func (s *Server) ClosePosition(ctx context.Context, in *lighv1.ClosePositionRequest) (*structpb.Value, error) {
	return s.rpcValue(ctx, "lighter_close_position", map[string]any{"market": in.GetMarket()})
}

func (s *Server) CalculateIndicator(ctx context.Context, in *lighv1.CalculateIndicatorRequest) (*structpb.Value, error) {
	return s.rpcValue(ctx, "lighter_calculate_indicator", map[string]any{
		"market":    in.GetMarket(),
		"indicator": in.GetIndicator(),
		"period":    int(in.GetPeriod()),
	})
}
