package main

import (
	"context"
	"fmt"
	"os"

	configSdk "github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/connectorrunner"
	"github.com/conductorone/baton-sdk/pkg/types"
	"github.com/conductorone/baton-zendesk/pkg/config"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"

	"github.com/conductorone/baton-zendesk/pkg/connector"
)

var (
	connectorName = "baton-zendesk"
	version       = "dev"
)

func main() {
	ctx := context.Background()

	_, cmd, err := configSdk.DefineConfiguration(
		ctx,
		connectorName,
		getConnector,
		config.Config,
		connectorrunner.WithDefaultCapabilitiesConnectorBuilderV2(&connector.Connector{}),
		connectorrunner.WithSessionStoreEnabled(),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	cmd.Version = version

	err = cmd.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func getConnector(ctx context.Context, cfg *config.Zendesk) (types.ConnectorServer, error) {
	logger := ctxzap.Extract(ctx)

	cb, err := connector.New(
		ctx,
		cfg.Orgs,
		cfg.Subdomain,
		cfg.Email,
		cfg.ApiToken,
	)
	if err != nil {
		logger.Error("error creating connector", zap.Error(err))
		return nil, err
	}

	c, err := connectorbuilder.NewConnector(ctx, cb)
	if err != nil {
		logger.Error("error creating connector", zap.Error(err))
		return nil, err
	}

	return c, nil
}
