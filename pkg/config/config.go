package config

import (
	"github.com/conductorone/baton-sdk/pkg/field"
)

var (
	SubdomainField = field.StringField(
		"subdomain",
		field.WithDescription("The Zendesk subdomain"),
		field.WithRequired(true),
	)
	ApiTokenField = field.StringField(
		"api-token",
		field.WithDescription("The Zendesk API token used to connect to the Zendesk API"),
		field.WithRequired(true),
	)
	EmailField = field.StringField(
		"email",
		field.WithDescription("The Zendesk email address for authentication"),
		field.WithRequired(true),
	)
	OrgsField = field.StringSliceField(
		"orgs",
		field.WithDescription("Limit syncing to specific organizations"),
	)

	ConfigurationFields = []field.SchemaField{
		SubdomainField,
		ApiTokenField,
		EmailField,
		OrgsField,
	}
)

//go:generate go run ./gen
var (
	Config = field.NewConfiguration(ConfigurationFields,
		field.WithConnectorDisplayName("Zendesk"),
		field.WithHelpUrl("/docs/baton/zendesk"),
		field.WithIconUrl("/static/app-icons/zendesk.svg"),
	)
)
