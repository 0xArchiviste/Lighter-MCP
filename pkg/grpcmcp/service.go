// Package grpcmcp implements the gRPC-MCP transport (https://github.com/0xArchiviste/gRPC-MCP).
// Service wiring is adapted from gRPC-MCP pkg/server under MIT license.
package grpcmcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	mcpv1 "github.com/0xarchiviste/lighter-mcp/gen/mcp/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Backend is the MCP application surface for the gRPC transport.
type Backend interface {
	Initialize(context.Context, *mcpv1.InitializeRequest) (*mcpv1.InitializeResponse, error)
	ListTools(context.Context, *mcpv1.ListToolsRequest) (*mcpv1.ListToolsResponse, error)
	CallTool(context.Context, *mcpv1.CallToolRequest, func(*mcpv1.CallToolEvent) error) error
	ListResources(context.Context, *mcpv1.ListResourcesRequest) (*mcpv1.ListResourcesResponse, error)
	ReadResource(context.Context, *mcpv1.ReadResourceRequest) (*mcpv1.ReadResourceResponse, error)
	SubscribeResource(context.Context, *mcpv1.SubscribeResourceRequest, func(*mcpv1.ResourceEvent) error) error
	ListPrompts(context.Context, *mcpv1.ListPromptsRequest) (*mcpv1.ListPromptsResponse, error)
	GetPrompt(context.Context, *mcpv1.GetPromptRequest) (*mcpv1.GetPromptResponse, error)
	Complete(context.Context, *mcpv1.CompleteRequest) (*mcpv1.CompleteResponse, error)
}

// Service implements mcp.v1.MCP and mcp.v1.AuthDiscovery.
type Service struct {
	mcpv1.UnimplementedMCPServer
	mcpv1.UnimplementedAuthDiscoveryServer

	Backend      Backend
	AuthMetadata *mcpv1.ProtectedResourceMetadata

	mu       sync.RWMutex
	sessions map[string]struct{}
}

// NewService constructs the gRPC MCP façade.
func NewService(backend Backend, authMeta *mcpv1.ProtectedResourceMetadata) *Service {
	return &Service{
		Backend:      backend,
		AuthMetadata: authMeta,
		sessions:     map[string]struct{}{},
	}
}

// Register attaches MCP + AuthDiscovery services to a gRPC server.
func (s *Service) Register(grpcServer *grpc.Server) {
	mcpv1.RegisterMCPServer(grpcServer, s)
	mcpv1.RegisterAuthDiscoveryServer(grpcServer, s)
}

func (s *Service) Initialize(ctx context.Context, req *mcpv1.InitializeRequest) (*mcpv1.InitializeResponse, error) {
	resp, err := s.Backend.Initialize(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.SessionId == "" {
		resp.SessionId = fmt.Sprintf("s-%d", time.Now().UnixNano())
	}
	s.mu.Lock()
	s.sessions[resp.SessionId] = struct{}{}
	s.mu.Unlock()
	return resp, nil
}

func (s *Service) Connect(stream grpc.BidiStreamingServer[mcpv1.Envelope, mcpv1.Envelope]) error {
	for {
		env, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		if env.GetNotification() != nil && env.GetMethod() == "notifications/initialized" {
			continue
		}

		req := env.GetRequest()
		if req == nil {
			continue
		}

		switch env.GetMethod() {
		case "ping":
			ping, _ := structpb.NewStruct(map[string]any{"ok": true, "ts": time.Now().UTC().Format(time.RFC3339Nano)})
			if err := stream.Send(&mcpv1.Envelope{
				Method: "ping",
				Kind: &mcpv1.Envelope_Response{
					Response: &mcpv1.JsonRpcResponse{
						Id:      req.Id,
						Outcome: &mcpv1.JsonRpcResponse_Result{Result: ping},
					},
				},
			}); err != nil {
				return err
			}
		default:
			if err := stream.Send(&mcpv1.Envelope{
				Method: env.GetMethod(),
				Kind: &mcpv1.Envelope_Response{
					Response: &mcpv1.JsonRpcResponse{
						Id: req.Id,
						Outcome: &mcpv1.JsonRpcResponse_Error{
							Error: &mcpv1.JsonRpcError{
								Code:    -32601,
								Message: "method not found",
							},
						},
					},
				},
			}); err != nil {
				return err
			}
		}
	}
}

func (s *Service) ListTools(ctx context.Context, req *mcpv1.ListToolsRequest) (*mcpv1.ListToolsResponse, error) {
	return s.Backend.ListTools(ctx, req)
}

func (s *Service) CallTool(req *mcpv1.CallToolRequest, stream grpc.ServerStreamingServer[mcpv1.CallToolEvent]) error {
	return s.Backend.CallTool(stream.Context(), req, stream.Send)
}

func (s *Service) ListResources(ctx context.Context, req *mcpv1.ListResourcesRequest) (*mcpv1.ListResourcesResponse, error) {
	return s.Backend.ListResources(ctx, req)
}

func (s *Service) ReadResource(ctx context.Context, req *mcpv1.ReadResourceRequest) (*mcpv1.ReadResourceResponse, error) {
	return s.Backend.ReadResource(ctx, req)
}

func (s *Service) SubscribeResource(req *mcpv1.SubscribeResourceRequest, stream grpc.ServerStreamingServer[mcpv1.ResourceEvent]) error {
	return s.Backend.SubscribeResource(stream.Context(), req, stream.Send)
}

func (s *Service) ListPrompts(ctx context.Context, req *mcpv1.ListPromptsRequest) (*mcpv1.ListPromptsResponse, error) {
	return s.Backend.ListPrompts(ctx, req)
}

func (s *Service) GetPrompt(ctx context.Context, req *mcpv1.GetPromptRequest) (*mcpv1.GetPromptResponse, error) {
	return s.Backend.GetPrompt(ctx, req)
}

func (s *Service) Complete(ctx context.Context, req *mcpv1.CompleteRequest) (*mcpv1.CompleteResponse, error) {
	return s.Backend.Complete(ctx, req)
}

func (s *Service) Ping(context.Context, *mcpv1.PingRequest) (*mcpv1.PingResponse, error) {
	return &mcpv1.PingResponse{ServerTime: timestamppb.Now()}, nil
}

func (s *Service) Shutdown(_ context.Context, _ *mcpv1.ShutdownRequest) (*mcpv1.ShutdownResponse, error) {
	return &mcpv1.ShutdownResponse{Accepted: true}, nil
}

func (s *Service) GetMetadata(context.Context, *mcpv1.GetMetadataRequest) (*mcpv1.ProtectedResourceMetadata, error) {
	if s.AuthMetadata == nil {
		return nil, status.Error(codes.NotFound, "auth metadata is not configured")
	}
	return s.AuthMetadata, nil
}
