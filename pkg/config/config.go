package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

const (
	AuthMethodAPIToken               = "api-token"
	AuthMethodOAuthClientCredentials = "oauth-client-credentials" //nolint:gosec // Auth method name, not a credential.
)

var (
	SubdomainField = field.StringField(
		"subdomain",
		field.WithDisplayName("Subdomain"),
		field.WithDescription("The Zendesk subdomain"),
		field.WithRequired(true),
	)
	ApiTokenField = field.StringField(
		"api-token",
		field.WithDisplayName("API Token"),
		field.WithDescription("The Zendesk API token used to connect to the Zendesk API"),
		field.WithRequired(true),
		field.WithIsSecret(true),
	)
	EmailField = field.StringField(
		"email",
		field.WithDisplayName("Email"),
		field.WithDescription("The Zendesk email address for authentication"),
		field.WithRequired(true),
	)
	OAuthClientIDField = field.StringField(
		"oauth-client-id",
		field.WithDisplayName("OAuth Client ID"),
		field.WithDescription("The unique identifier of the Zendesk OAuth client"),
		field.WithRequired(true),
	)
	OAuthClientSecretField = field.StringField(
		"oauth-client-secret",
		field.WithDisplayName("OAuth Client Secret"),
		field.WithDescription("The secret of the Zendesk OAuth client"),
		field.WithRequired(true),
		field.WithIsSecret(true),
	)
	OAuthScopesField = field.StringSliceField(
		"oauth-scopes",
		field.WithDisplayName("OAuth Scopes"),
		field.WithDescription("The scopes requested for the OAuth access token. Use only \"read\" for sync-only connections"),
		field.WithDefaultValue([]string{"read", "write"}),
	)
	OrgsField = field.StringSliceField(
		"orgs",
		field.WithDisplayName("Organizations"),
		field.WithDescription("Limit syncing to specific organizations"),
	)
	BaseURLField = field.StringField(
		"base-url",
		field.WithDisplayName("Base URL"),
		field.WithDescription("Override the Zendesk API URL (for testing)"),
		field.WithHidden(true),
		field.WithExportTarget(field.ExportTargetCLIOnly),
	)
	// TicketingGUIField re-exports the SDK's shared --ticketing flag so the
	// ConductorOne GUI shows the toggle (same pattern as baton-jira and
	// baton-freshservice).
	TicketingGUIField = field.TicketingField.ExportAs(field.ExportTargetGUI)

	ConfigurationFields = []field.SchemaField{
		SubdomainField,
		ApiTokenField,
		EmailField,
		OAuthClientIDField,
		OAuthClientSecretField,
		OAuthScopesField,
		OrgsField,
		BaseURLField,
		TicketingGUIField,
	}

	// Shared fields must be listed in every group; the SDK skips validation of fields outside the selected group.
	FieldGroups = []field.SchemaFieldGroup{
		{
			Name:        AuthMethodOAuthClientCredentials,
			DisplayName: "OAuth client credentials",
			HelpText: "In Zendesk Admin Center go to Apps and integrations > APIs > OAuth clients and create a Confidential client. " +
				"Copy the identifier and secret. The client must be created by a Zendesk admin; ConductorOne actions are attributed to that user.",
			Fields: []field.SchemaField{
				SubdomainField,
				OAuthClientIDField,
				OAuthClientSecretField,
				OAuthScopesField,
				OrgsField,
				BaseURLField,
				TicketingGUIField,
			},
		},
		{
			Name:        AuthMethodAPIToken,
			DisplayName: "API token (deprecated by Zendesk 2027-04-30)",
			HelpText: "Zendesk no longer allows new API tokens on accounts created after 28 July 2026, and on any account from 27 October 2026. " +
				"Existing tokens stop working on 30 April 2027. Use OAuth client credentials instead.",
			Default: true,
			Fields: []field.SchemaField{
				SubdomainField,
				EmailField,
				ApiTokenField,
				OrgsField,
				BaseURLField,
				TicketingGUIField,
			},
		},
	}
)

//go:generate go run ./gen
var (
	Config = field.NewConfiguration(ConfigurationFields,
		field.WithConnectorDisplayName("Zendesk"),
		field.WithHelpUrl("/docs/baton/zendesk"),
		field.WithIconUrl("/static/app-icons/zendesk.svg"),
		field.WithFieldGroups(FieldGroups),
	)
)
