package connector

import (
	"context"
	"fmt"
	"io"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/cli"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/uhttp"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"google.golang.org/grpc/codes"
)

type Connector struct {
	orgs          []string
	zendeskClient *client.ZendeskClient
	subdomain     string
	// skipOrgResourceType reports whether the "org" resource type has been
	// excluded from this sync via the configured sync filter.
	//
	// Named for the SKIP condition rather than the sync condition so the zero
	// value (false, "don't skip") is the safe default. main.go registers a
	// zero-value Connector{} as the capabilities factory
	// (connectorrunner.WithDefaultCapabilitiesConnectorBuilderV2), which never
	// goes through New; a syncOrgResourceType-shaped bool would read false
	// there and wrongly report org as filtered out in baton_capabilities.json.
	//
	// teamMemberBuilder uses this to annotate the team_member resource type so
	// the SDK skips Entitlements()/Grants() entirely when org is out of scope.
	skipOrgResourceType bool
}

// ResourceSyncers returns a ResourceSyncerV2 for each resource type that should be synced from the upstream service.
func (d *Connector) ResourceSyncers(_ context.Context) []connectorbuilder.ResourceSyncerV2 {
	return []connectorbuilder.ResourceSyncerV2{
		groupBuilder(d.zendeskClient),
		orgBuilder(d.zendeskClient, d.orgs),
		roleBuilder(d.zendeskClient),
		teamMemberBuilder(d.zendeskClient, d.orgs, d.skipOrgResourceType),
	}
}

// Close cleans up any resources held by the connector.
func (d *Connector) Close() error {
	return nil
}

// Asset takes an input AssetRef and attempts to fetch it using the connector's authenticated http client
// It streams a response, always starting with a metadata object, following by chunked payloads for the asset.
func (d *Connector) Asset(_ context.Context, _ *v2.AssetRef) (string, io.ReadCloser, error) {
	return "", nil, nil
}

// Metadata returns metadata about the connector.
func (d *Connector) Metadata(_ context.Context) (*v2.ConnectorMetadata, error) {
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
	me, err := d.zendeskClient.GetCurrentUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("baton-zendesk: validate credentials: %w", err)
	}
	// Zendesk answers /users/me with an anonymous end-user instead of an error for some unauthenticated requests.
	if me.Role != teamRoleAdmin {
		return nil, uhttp.WrapErrors(codes.PermissionDenied, fmt.Sprintf(
			"baton-zendesk: the credentials authenticate as user %d with role %q, but a Zendesk admin is required; "+
				"for OAuth, the client must be created by an admin", me.ID, me.Role))
	}
	return nil, nil
}

// New returns a new instance of the connector.
func New(ctx context.Context, zendeskOrgs []string, subdomain string, baseURL string, auth client.AuthConfig, opts *cli.ConnectorOpts) (*Connector, error) {
	zc, err := client.New(ctx, nil, subdomain, baseURL, auth)
	if err != nil {
		return nil, err
	}

	skipOrgResourceType := opts != nil && !opts.WillSyncResourceType(OrgResourceTypeID)

	return &Connector{
		zendeskClient:       zc,
		orgs:                zendeskOrgs,
		subdomain:           subdomain,
		skipOrgResourceType: skipOrgResourceType,
	}, nil
}
