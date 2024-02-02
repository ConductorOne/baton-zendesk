package main

import (
	"context"
	"errors"

	"github.com/conductorone/baton-sdk/pkg/cli"
)

// config defines the external configuration required for the connector to run.
type config struct {
	cli.BaseConfig `mapstructure:",squash"` // Puts the base config options in the same place as the connector options
	Subdomain      string                   `mapstructure:"subdomain"`
	ApiToken       string                   `mapstructure:"api-token"`
}

// validateConfig is run after the configuration is loaded, and should return an error if it isn't valid.
func validateConfig(ctx context.Context, cfg *config) error {
	if cfg.Subdomain == "" {
		return errors.New("Subdomain is required")
	}
	if cfg.ApiToken == "" {
		return errors.New("Api-Token is required")
	}
	return nil
}
