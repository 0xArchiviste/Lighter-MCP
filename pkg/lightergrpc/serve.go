// Package lightergrpc hosts gRPC-MCP (mcp.v1) and the Lighter SDK (lighter.v1.Lighter) on one listener.
package lightergrpc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"

	mcpv1 "github.com/0xarchiviste/lighter-mcp/gen/mcp/v1"
	lighv1 "github.com/0xarchiviste/lighter-mcp/gen/lighter/v1"
	"github.com/0xarchiviste/lighter-mcp/pkg/grpcmcp"
	"github.com/0xarchiviste/lighter-mcp/pkg/lighterapp"
	"github.com/0xarchiviste/lighter-mcp/pkg/sdkgrpc"
	grpcauth "github.com/0xArchiviste/gRPC-MCP/pkg/auth"
	"google.golang.org/grpc"
)

// Serve listens on addr and serves MCP + Lighter SDK until the process exits or Serve returns an error.
func Serve(app *lighterapp.App, addr, authMode, apiKey string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	backend := grpcmcp.NewLighterBackend(app)
	authMeta := &mcpv1.ProtectedResourceMetadata{
		Resource:               "lighter-grpc://" + addr,
		AuthorizationServers:   []string{"https://issuer.example.com"},
		ScopesSupported:        []string{"mcp:tools.call", "mcp:tools.list", "mcp:resources.read", "lighter:rpc"},
		JwksUri:                "https://issuer.example.com/.well-known/jwks.json",
		BearerMethodsSupported: []string{"header"},
	}
	mcpSvc := grpcmcp.NewService(backend, authMeta)

	policies := map[string]grpcauth.AuthPolicy{
		mcpv1.MCP_ListTools_FullMethodName:          {Method: mcpv1.MCP_ListTools_FullMethodName, RequiredScopes: []string{"mcp:tools.list"}},
		mcpv1.MCP_CallTool_FullMethodName:           {Method: mcpv1.MCP_CallTool_FullMethodName, RequiredScopes: []string{"mcp:tools.call"}},
		mcpv1.MCP_ListResources_FullMethodName:     {Method: mcpv1.MCP_ListResources_FullMethodName, RequiredScopes: []string{"mcp:resources.read"}},
		mcpv1.MCP_ReadResource_FullMethodName:      {Method: mcpv1.MCP_ReadResource_FullMethodName, RequiredScopes: []string{"mcp:resources.read"}},
		mcpv1.MCP_SubscribeResource_FullMethodName: {Method: mcpv1.MCP_SubscribeResource_FullMethodName, RequiredScopes: []string{"mcp:resources.read"}},
		lighv1.Lighter_GetBalance_FullMethodName:         {Method: lighv1.Lighter_GetBalance_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_ListMarkets_FullMethodName:       {Method: lighv1.Lighter_ListMarkets_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_GetMarket_FullMethodName:         {Method: lighv1.Lighter_GetMarket_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_ListPositions_FullMethodName:     {Method: lighv1.Lighter_ListPositions_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_PlaceLimitOrder_FullMethodName:   {Method: lighv1.Lighter_PlaceLimitOrder_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_CancelOrder_FullMethodName:      {Method: lighv1.Lighter_CancelOrder_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_SetTPSL_FullMethodName:           {Method: lighv1.Lighter_SetTPSL_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_ClosePosition_FullMethodName:     {Method: lighv1.Lighter_ClosePosition_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_CalculateIndicator_FullMethodName: {Method: lighv1.Lighter_CalculateIndicator_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
	}

	cfgAuth := grpcauth.Config{
		Policies:        policies,
		AllowInsecure:   authMode == "none" || authMode == "mtls",
		WWWAuthenticate: `Bearer resource_metadata="mcp.v1.AuthDiscovery/GetMetadata"`,
	}
	if authMode == "apikey" {
		cfgAuth.APIKeys = map[string]string{apiKey: "apikey-user"}
	}
	if authMode == "bearer" {
		cfgAuth.Verifier = &staticBearerVerifier{principal: &grpcauth.Principal{
			Subject: "bearer-user",
			Method:  "bearer",
			Scopes: map[string]struct{}{
				"mcp:tools.list":     {},
				"mcp:tools.call":     {},
				"mcp:resources.read": {},
				"lighter:rpc":        {},
				"mcp:*":              {},
			},
		}}
	}

	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(grpcauth.UnaryInterceptor(cfgAuth)),
		grpc.StreamInterceptor(grpcauth.StreamInterceptor(cfgAuth)),
	}
	s := grpc.NewServer(opts...)
	mcpSvc.Register(s)
	lighv1.RegisterLighterServer(s, sdkgrpc.NewServer(app))

	log.Printf("gRPC listening on %s (auth=%s) — gRPC-MCP mcp.v1.MCP + lighter.v1.Lighter SDK", addr, authMode)
	return s.Serve(lis)
}

type staticBearerVerifier struct {
	principal *grpcauth.Principal
}

func (v *staticBearerVerifier) VerifyBearer(_ context.Context, token string) (*grpcauth.Principal, error) {
	if token == "" {
		return nil, errors.New("empty bearer token")
	}
	return v.principal, nil
}
