package grpcmcp

import (
	"context"
	"encoding/json"

	mcpv1 "github.com/0xarchiviste/lighter-mcp/gen/mcp/v1"
	"github.com/0xarchiviste/lighter-mcp/pkg/lighterapp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// LighterBackend implements the gRPC-MCP Backend for Lighter tools.
type LighterBackend struct {
	App *lighterapp.App
}

// NewLighterBackend wraps the shared lighter application.
func NewLighterBackend(app *lighterapp.App) *LighterBackend {
	return &LighterBackend{App: app}
}

func (b *LighterBackend) Initialize(context.Context, *mcpv1.InitializeRequest) (*mcpv1.InitializeResponse, error) {
	clientCaps, _ := structpb.NewStruct(map[string]any{})
	serverCaps, _ := structpb.NewStruct(map[string]any{
		"tools":     map[string]any{"listChanged": false},
		"resources": map[string]any{"subscribe": false, "listChanged": false},
		"prompts":   map[string]any{"listChanged": false},
		"logging":   map[string]any{},
	})
	_ = clientCaps
	return &mcpv1.InitializeResponse{
		ProtocolVersion: "2025-06-18",
		Capabilities:    &mcpv1.CapabilitySet{Values: serverCaps},
		ServerInfo: &mcpv1.ServerInfo{
			Name:    "lighter-mcp",
			Title:   "Lighter MCP (gRPC transport)",
			Version: "0.2.0",
		},
		Instructions: "Lighter.xyz MCP over gRPC-MCP. Use typed ListTools / CallTool RPCs; optional Connect stream for JSON-RPC envelopes per gRPC-MCP spec.",
	}, nil
}

func (b *LighterBackend) ListTools(context.Context, *mcpv1.ListToolsRequest) (*mcpv1.ListToolsResponse, error) {
	tools := []*mcpv1.Tool{
		tool("lighter_get_balance", "Fetch account balance and collateral.", map[string]any{"type": "object", "properties": map[string]any{}}),
		tool("lighter_list_markets", "List markets with prices.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "max markets (default 10)"},
			},
		}),
		tool("lighter_get_market", "Get one market by symbol.", map[string]any{
			"type":       "object",
			"required":   []any{"symbol"},
			"properties": map[string]any{"symbol": map[string]any{"type": "string"}},
		}),
		tool("lighter_list_positions", "List positions.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"market": map[string]any{"type": "string", "description": "optional filter"}},
		}),
		tool("lighter_place_limit_order", "Place a GTT limit order.", map[string]any{
			"type":     "object",
			"required": []any{"market", "side", "price", "size"},
			"properties": map[string]any{
				"market": map[string]any{"type": "string"},
				"side":   map[string]any{"type": "string", "enum": []any{"buy", "sell", "BUY", "SELL"}},
				"price":  map[string]any{"type": "number"},
				"size":   map[string]any{"type": "number"},
			},
		}),
		tool("lighter_cancel_order", "Cancel by client order index.", map[string]any{
			"type":     "object",
			"required": []any{"market", "client_order_index"},
			"properties": map[string]any{
				"market":               map[string]any{"type": "string"},
				"client_order_index": map[string]any{"type": "integer"},
			},
		}),
		tool("lighter_set_tp_sl", "Set take-profit and stop-loss.", map[string]any{
			"type":     "object",
			"required": []any{"market"},
			"properties": map[string]any{
				"market":   map[string]any{"type": "string"},
				"tp_price": map[string]any{"type": "number"},
				"sl_price": map[string]any{"type": "number"},
			},
		}),
		tool("lighter_close_position", "Close a position.", map[string]any{
			"type":       "object",
			"required":   []any{"market"},
			"properties": map[string]any{"market": map[string]any{"type": "string"}},
		}),
		tool("lighter_calculate_indicator", "Technical indicators.", map[string]any{
			"type":     "object",
			"required": []any{"market", "indicator"},
			"properties": map[string]any{
				"market":    map[string]any{"type": "string"},
				"indicator": map[string]any{"type": "string"},
				"period":    map[string]any{"type": "integer"},
			},
		}),
	}
	return &mcpv1.ListToolsResponse{Tools: tools}, nil
}

func tool(name, desc string, schema map[string]any) *mcpv1.Tool {
	st, err := structpb.NewStruct(schema)
	if err != nil {
		st, _ = structpb.NewStruct(map[string]any{"type": "object"})
	}
	return &mcpv1.Tool{Name: name, Description: desc, InputSchema: st}
}

func (b *LighterBackend) CallTool(ctx context.Context, req *mcpv1.CallToolRequest, send func(*mcpv1.CallToolEvent) error) error {
	args := map[string]any{}
	if req.GetArguments() != nil {
		args = req.GetArguments().AsMap()
	}
	text, isErr, err := b.App.DispatchTool(ctx, req.GetName(), args)
	if err != nil {
		return send(&mcpv1.CallToolEvent{
			Kind: &mcpv1.CallToolEvent_Error{
				Error: &mcpv1.JsonRpcError{Code: -32603, Message: err.Error()},
			},
		})
	}
	return send(&mcpv1.CallToolEvent{
		Kind: &mcpv1.CallToolEvent_Result{
			Result: &mcpv1.CallToolResult{
				Content: []*mcpv1.Content{{
					Kind: &mcpv1.Content_Text{
						Text: &mcpv1.TextContent{Text: text, MimeType: "application/json"},
					},
				}},
				IsError: isErr,
			},
		},
	})
}

func (b *LighterBackend) ListResources(context.Context, *mcpv1.ListResourcesRequest) (*mcpv1.ListResourcesResponse, error) {
	return &mcpv1.ListResourcesResponse{Resources: nil}, nil
}

func (b *LighterBackend) ReadResource(context.Context, *mcpv1.ReadResourceRequest) (*mcpv1.ReadResourceResponse, error) {
	return nil, status.Error(codes.NotFound, "resources not implemented")
}

func (b *LighterBackend) SubscribeResource(context.Context, *mcpv1.SubscribeResourceRequest, func(*mcpv1.ResourceEvent) error) error {
	return status.Error(codes.Unimplemented, "resource subscription not implemented")
}

func (b *LighterBackend) ListPrompts(context.Context, *mcpv1.ListPromptsRequest) (*mcpv1.ListPromptsResponse, error) {
	return &mcpv1.ListPromptsResponse{Prompts: nil}, nil
}

func (b *LighterBackend) GetPrompt(context.Context, *mcpv1.GetPromptRequest) (*mcpv1.GetPromptResponse, error) {
	return nil, status.Error(codes.NotFound, "prompts not implemented")
}

func (b *LighterBackend) Complete(context.Context, *mcpv1.CompleteRequest) (*mcpv1.CompleteResponse, error) {
	return &mcpv1.CompleteResponse{}, nil
}

// JSONToProtoValue unmarshals JSON text into a google.protobuf.Value.
func JSONToProtoValue(jsonText string) (*structpb.Value, error) {
	var raw any
	if err := json.Unmarshal([]byte(jsonText), &raw); err != nil {
		return nil, err
	}
	return structpb.NewValue(raw)
}
