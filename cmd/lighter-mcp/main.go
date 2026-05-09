// Command lighter-mcp serves Lighter.xyz over stdio MCP and/or gRPC (gRPC-MCP + SDK).
package main

import (
	"context"
	"flag"
	"log"

	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/lighterapp"
	"github.com/0xarchiviste/lighter-mcp/pkg/lightergrpc"
	"github.com/0xarchiviste/lighter-mcp/pkg/walletstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	var err error
	cfg, err = walletstore.Resolve(context.Background(), cfg)
	if err != nil {
		log.Fatalf("wallet resolve: %v", err)
	}
	if cfg.APIKeyPrivateKey == "" {
		log.Fatal("LIGHTER_API_KEY_PRIVATE_KEY is required (or use LIGHTER_WALLET_BACKEND=supabase with wallet env)")
	}
	if cfg.AccountIndex == 0 {
		log.Fatal("LIGHTER_ACCOUNT_INDEX must be set to a non-zero account index (or select a supabase wallet row)")
	}

	var app *lighterapp.App
	app, err = lighterapp.NewApp(cfg)
	if err != nil {
		log.Fatalf("app: %v", err)
	}

	switch *transport {
	case "stdio":
		runStdio(app)
	case "grpc":
		if err := lightergrpc.Serve(app, *grpcAddr, *grpcAuth, *apiKey); err != nil {
			log.Fatal(err)
		}
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
