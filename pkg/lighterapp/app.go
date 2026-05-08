// Package lighterapp wires Lighter API clients for MCP and gRPC surfaces.
package lighterapp

import (
	"context"

	"github.com/0xarchiviste/lighter-mcp/pkg/account"
	"github.com/0xarchiviste/lighter-mcp/pkg/api"
	"github.com/0xarchiviste/lighter-mcp/pkg/config"
	"github.com/0xarchiviste/lighter-mcp/pkg/indicators"
	"github.com/0xarchiviste/lighter-mcp/pkg/positions"
	"github.com/0xarchiviste/lighter-mcp/pkg/signer"
	tpsl "github.com/0xarchiviste/lighter-mcp/pkg/tp-sl"
)

// App holds shared Lighter clients used by stdio MCP, gRPC-MCP, and the SDK service.
type App struct {
	Cfg              config.Config
	APIClient        *api.Client
	Signer           *signer.Signer
	OrderClient      *account.OrderPlacementClient
	PositionsClient  *positions.PositionsClient
	TPSLClient       *tpsl.TPSLClient
	IndicatorService *indicators.IndicatorService
}

// NewApp builds clients from loaded config (same requirements as the stdio server).
func NewApp(cfg config.Config) (*App, error) {
	apiClient := api.New(cfg)
	s, err := signer.New(signer.Config{
		BaseURL:          cfg.BaseURL,
		APIKeyPrivateKey: cfg.APIKeyPrivateKey,
		AccountIndex:     cfg.AccountIndex,
		APIKeyIndex:      cfg.APIKeyIndex,
	})
	if err != nil {
		return nil, err
	}
	s.SetNonceProvider(apiClient)

	orderClient, err := account.NewOrderPlacementClient(apiClient, cfg)
	if err != nil {
		return nil, err
	}
	positionsClient, err := positions.NewPositionsClient(apiClient, cfg)
	if err != nil {
		return nil, err
	}
	tpslClient, err := tpsl.NewTPSLClient(apiClient, cfg)
	if err != nil {
		return nil, err
	}

	return &App{
		Cfg:              cfg,
		APIClient:        apiClient,
		Signer:           s,
		OrderClient:      orderClient,
		PositionsClient:  positionsClient,
		TPSLClient:       tpslClient,
		IndicatorService: indicators.NewIndicatorService(apiClient),
	}, nil
}

// AuthToken returns a short-lived Lighter auth string for REST calls.
func (a *App) AuthToken(ctx context.Context) (string, error) {
	return a.Signer.CreateAuthTokenWithExpiry(ctx, 3600)
}
