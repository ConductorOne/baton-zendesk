package connector

import (
	"context"
	"io"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/cli"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-zendesk/pkg/client"
)

type Connector struct {
	orgs          []string
	zendeskClient *client.ZendeskClient
	subdomain     string
	email         string
	apiToken      string
	baseURL       string
	// syncOrgs reports whether the "org" resource type is in scope for this
	// sync, per the configured sync filter (or true when no filter was set).
	// team_member.Grants uses this to gate its cross-type org grant emission
	// (see teamMemberBuilder and teamMemberResourceType.Grants).
	syncOrgs bool
}

// ResourceSyncers returns a ResourceSyncerV2 for each resource type that should be synced from the upstream service.
func (d *Connector) ResourceSyncers(ctx context.Context) []connectorbuilder.ResourceSyncerV2 {
	return []connectorbuilder.ResourceSyncerV2{
		groupBuilder(d.zendeskClient),
		orgBuilder(d.zendeskClient, d.orgs),
		roleBuilder(d.zendeskClient),
		teamMemberBuilder(d.zendeskClient, d.orgs, d.syncOrgs),
	}
}

// Close cleans up any resources held by the connector.
func (d *Connector) Close() error {
	return nil
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
		Description: "Connector syncing users, groups, and roles from Zendesk.",
		AccountCreationSchema: &v2.ConnectorAccountCreationSchema{
			FieldMap: map[string]*v2.ConnectorAccountCreationSchema_Field{
				"name": {
					DisplayName: "Name",
					Required:    true,
					Description: "The name of the user.",
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Placeholder: "Name",
					Order:       1,
				},
				"email": {
					DisplayName: "Email",
					Required:    false,
					Description: "The email of the user.",
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Placeholder: "Email",
					Order:       2,
				},
				"role": {
					DisplayName: roleDisplay,
					Required:    false,
					Description: "The role of the user.",
					Field: &v2.ConnectorAccountCreationSchema_Field_StringField{
						StringField: &v2.ConnectorAccountCreationSchema_StringField{},
					},
					Placeholder: "Role",
					Order:       3,
				},
			},
		},
	}, nil
}

// Validate is called to ensure that the connector is properly configured. It should exercise any API credentials
// to be sure that they are valid.
func (d *Connector) Validate(ctx context.Context) (annotations.Annotations, error) {
	return nil, nil
}

// New returns a new instance of the connector.
func New(ctx context.Context, zendeskOrgs []string, subdomain string, email string, apiToken string, baseURL string, opts *cli.ConnectorOpts) (*Connector, error) {
	var zc *client.ZendeskClient
	if apiToken != "" {
		var err error
		zc, err = client.New(ctx, nil, subdomain, email, apiToken, baseURL)
		if err != nil {
			return nil, err
		}
	}

	syncOrgs := opts == nil || opts.WillSyncResourceType(OrgResourceTypeID)

	return &Connector{
		zendeskClient: zc,
		orgs:          zendeskOrgs,
		subdomain:     subdomain,
		email:         email,
		apiToken:      apiToken,
		baseURL:       baseURL,
		syncOrgs:      syncOrgs,
	}, nil
}
