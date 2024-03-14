package connector

import (
	"context"
	"io"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-zendesk/pkg/client"
)

type Connector struct {
	orgs          []string
	zendeskClient *client.ZendeskClient
}

// ResourceSyncers returns a ResourceSyncer for each resource type that should be synced from the upstream service.
func (d *Connector) ResourceSyncers(ctx context.Context) []connectorbuilder.ResourceSyncer {
	return []connectorbuilder.ResourceSyncer{
		groupBuilder(d.zendeskClient),
		orgBuilder(d.zendeskClient, d.orgs),
		roleBuilder(d.zendeskClient),
		teamBuilder(d.zendeskClient),
	}
}

// Asset takes an input AssetRef and attempts to fetch it using the connector's authenticated http client
// It streams a response, always starting with a metadata object, following by chunked payloads for the asset.
func (d *Connector) Asset(ctx context.Context, asset *v2.AssetRef) (string, io.ReadCloser, error) {
	return "", nil, nil
}

// Metadata returns metadata about the connector.
func (d *Connector) Metadata(ctx context.Context) (*v2.ConnectorMetadata, error) {
	return &v2.ConnectorMetadata{
		DisplayName: "Zendesk Connector",
		Description: "Connector syncing users, groups, and roles from Zendesk..",
	}, nil
}

// Validate is called to ensure that the connector is properly configured. It should exercise any API credentials
// to be sure that they are valid.
func (d *Connector) Validate(ctx context.Context) (annotations.Annotations, error) {
	return nil, nil
}

// New returns a new instance of the connector.
func New(ctx context.Context, zendeskOrgs []string, subdomain string, email string, apiToken string) (*Connector, error) {
	zc, err := client.New(ctx, nil, subdomain, email, apiToken)
	if err != nil {
		return nil, err
	}

	return &Connector{
		zendeskClient: zc,
		orgs:          zendeskOrgs,
	}, nil
}
