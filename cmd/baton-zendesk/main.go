package main

import (
	"context"

	"github.com/conductorone/baton-sdk/pkg/cli"
	configSdk "github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/connectorrunner"
	"github.com/conductorone/baton-zendesk/pkg/config"
	"github.com/conductorone/baton-zendesk/pkg/connector"
)

var (
	connectorName = "baton-zendesk"
	version       = "dev"
)

func main() {
	ctx := context.Background()
	configSdk.RunConnector(
		ctx,
		connectorName,
		version,
		config.Config,
		getConnector,
		connectorrunner.WithDefaultCapabilitiesConnectorBuilderV2(&connector.Connector{}),
		connectorrunner.WithSessionStoreEnabled(),
	)
}

func getConnector(ctx context.Context, cfg *config.Zendesk, _ *cli.ConnectorOpts) (connectorbuilder.ConnectorBuilderV2, []connectorbuilder.Opt, error) {
	cb, err := connector.New(ctx, cfg.Orgs, cfg.Subdomain, cfg.Email, cfg.ApiToken, cfg.BaseUrl)
	if err != nil {
		return nil, nil, err
	}
	return cb, nil, nil
}
