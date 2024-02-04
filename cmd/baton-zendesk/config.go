package main

import (
	"context"
	"errors"

	"github.com/conductorone/baton-sdk/pkg/cli"
	"github.com/spf13/cobra"
)

// config defines the external configuration required for the connector to run.
type config struct {
	cli.BaseConfig `mapstructure:",squash"` // Puts the base config options in the same place as the connector options
	Subdomain      string                   `mapstructure:"subdomain"`
	ApiToken       string                   `mapstructure:"api-token"`
	Email          string                   `mapstructure:"email"`
}

// validateConfig is run after the configuration is loaded, and should return an error if it isn't valid.
func validateConfig(ctx context.Context, cfg *config) error {
	if cfg.Subdomain == "" {
		return errors.New("subdomain is required")
	}
	if cfg.ApiToken == "" {
		return errors.New("api-Token is required")
	}

	return nil
}

// cmdFlags sets the cmdFlags required for the connector.
func cmdFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String("subdomain", "", "The Zendesk subdomain. ($BATON_SUBDOMAIN)")
	cmd.PersistentFlags().String("api-token", "", "The Zendesk apitoken. ($BATON_API_TOKEN)")
	cmd.PersistentFlags().String("email", "", "The Zendesk email. ($BATON_EMAIL)")
}
