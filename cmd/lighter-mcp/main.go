// Command lighter-mcp serves Lighter.xyz over stdio MCP and/or gRPC (gRPC-MCP + SDK).
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"

	mcpv1 "github.com/0xarchiviste/lighter-mcp/gen/mcp/v1"
	lighv1 "github.com/0xarchiviste/lighter-mcp/gen/lighter/v1"
	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/grpcmcp"
	"github.com/0xarchiviste/lighter-mcp/pkg/lighterapp"
	"github.com/0xarchiviste/lighter-mcp/pkg/sdkgrpc"
	grpcauth "github.com/0xArchiviste/gRPC-MCP/pkg/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
)

func main() {
	var (
		transport = flag.String("transport", "stdio", "stdio (default) or grpc")
		grpcAddr  = flag.String("grpc-addr", "127.0.0.1:9090", "listen address when -transport=grpc")
		grpcAuth  = flag.String("grpc-auth", "none", "grpc auth: none|bearer|apikey (uses gRPC-MCP pkg/auth)")
		apiKey    = flag.String("grpc-api-key", "dev-key", "expected x-mcp-api-key when -grpc-auth=apikey")
	)
	flag.Parse()

	cfg := config.Load()
	if cfg.APIKeyPrivateKey == "" {
		log.Fatal("LIGHTER_API_KEY_PRIVATE_KEY is required")
	}
	if cfg.AccountIndex == 0 {
		log.Fatal("LIGHTER_ACCOUNT_INDEX must be set to a non-zero account index")
	}

	app, err := lighterapp.NewApp(cfg)
	if err != nil {
		log.Fatalf("app: %v", err)
	}

	switch *transport {
	case "stdio":
		runStdio(app)
	case "grpc":
		runGRPC(app, *grpcAddr, *grpcAuth, *apiKey)
	default:
		log.Fatalf("unknown -transport %q (use stdio or grpc)", *transport)
	}
}

func runStdio(app *lighterapp.App) {
	server := mcp.NewServer(&mcp.Implementation{Name: "lighter-mcp", Version: "0.2.0"}, nil)
	lighterapp.RegisterStdioTools(server, app)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func runGRPC(app *lighterapp.App, addr, authMode, apiKey string) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	backend := grpcmcp.NewLighterBackend(app)
	authMeta := &mcpv1.ProtectedResourceMetadata{
		Resource:               "lighter-mcp-grpc://" + addr,
		AuthorizationServers:   []string{"https://issuer.example.com"},
		ScopesSupported:        []string{"mcp:tools.call", "mcp:tools.list", "mcp:resources.read", "lighter:rpc"},
		JwksUri:                "https://issuer.example.com/.well-known/jwks.json",
		BearerMethodsSupported: []string{"header"},
	}
	mcpSvc := grpcmcp.NewService(backend, authMeta)

	policies := map[string]grpcauth.AuthPolicy{
		mcpv1.MCP_ListTools_FullMethodName:         {Method: mcpv1.MCP_ListTools_FullMethodName, RequiredScopes: []string{"mcp:tools.list"}},
		mcpv1.MCP_CallTool_FullMethodName:         {Method: mcpv1.MCP_CallTool_FullMethodName, RequiredScopes: []string{"mcp:tools.call"}},
		mcpv1.MCP_ListResources_FullMethodName:    {Method: mcpv1.MCP_ListResources_FullMethodName, RequiredScopes: []string{"mcp:resources.read"}},
		mcpv1.MCP_ReadResource_FullMethodName:     {Method: mcpv1.MCP_ReadResource_FullMethodName, RequiredScopes: []string{"mcp:resources.read"}},
		mcpv1.MCP_SubscribeResource_FullMethodName: {Method: mcpv1.MCP_SubscribeResource_FullMethodName, RequiredScopes: []string{"mcp:resources.read"}},
		lighv1.Lighter_GetBalance_FullMethodName:         {Method: lighv1.Lighter_GetBalance_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_ListMarkets_FullMethodName:        {Method: lighv1.Lighter_ListMarkets_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_GetMarket_FullMethodName:          {Method: lighv1.Lighter_GetMarket_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_ListPositions_FullMethodName:      {Method: lighv1.Lighter_ListPositions_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_PlaceLimitOrder_FullMethodName:    {Method: lighv1.Lighter_PlaceLimitOrder_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_CancelOrder_FullMethodName:        {Method: lighv1.Lighter_CancelOrder_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
		lighv1.Lighter_SetTPSL_FullMethodName:            {Method: lighv1.Lighter_SetTPSL_FullMethodName, RequiredScopes: []string{"lighter:rpc"}},
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
		cfgAuth.Verifier = &staticVerifier{principal: &grpcauth.Principal{
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

	log.Printf("lighter-mcp gRPC listening on %s (auth=%s) — MCP per github.com/0xArchiviste/gRPC-MCP + lighter.v1.Lighter SDK", addr, authMode)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("grpc: %v", err)
	}
}

type staticVerifier struct {
	principal *grpcauth.Principal
}

func (v *staticVerifier) VerifyBearer(_ context.Context, token string) (*grpcauth.Principal, error) {
	if token == "" {
		return nil, errors.New("empty bearer token")
	}
	return v.principal, nil
}
