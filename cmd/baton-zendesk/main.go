package main

import (
	"context"

	"github.com/conductorone/baton-sdk/pkg/cli"
	configSdk "github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/connectorrunner"
	"github.com/conductorone/baton-zendesk/pkg/client"
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

func getConnector(ctx context.Context, cfg *config.Zendesk, opts *cli.ConnectorOpts) (connectorbuilder.ConnectorBuilderV2, []connectorbuilder.Opt, error) {
	auth := client.AuthConfig{
		Email:             cfg.Email,
		APIToken:          cfg.ApiToken,
		OAuthClientID:     cfg.OauthClientId,
		OAuthClientSecret: cfg.OauthClientSecret,
		OAuthScopes:       cfg.OauthScopes,
	}
	// Any other value falls back to the default api-token group, matching the SDK's field-group validation.
	if opts != nil && opts.SelectedAuthMethod == config.AuthMethodOAuthClientCredentials {
		auth.OAuth = true
	}
	cb, err := connector.New(ctx, cfg.Orgs, cfg.Subdomain, cfg.BaseUrl, auth, opts)
	if err != nil {
		return nil, nil, err
	}
	var builderOpts []connectorbuilder.Opt
	if cfg.Ticketing {
		builderOpts = append(builderOpts, connectorbuilder.WithTicketingEnabled())
	}
	return cb, builderOpts, nil
}
