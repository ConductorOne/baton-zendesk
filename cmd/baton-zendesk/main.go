package main

import (
	"context"
	"fmt"
	"os"

	configSdk "github.com/conductorone/baton-sdk/pkg/config"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/types"
	"github.com/conductorone/baton-zendesk/pkg/config"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/spf13/viper"
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
		config.Configuration,
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

func getConnector(ctx context.Context, v *viper.Viper) (types.ConnectorServer, error) {
	logger := ctxzap.Extract(ctx)

	cb, err := connector.New(
		ctx,
		v.GetStringSlice(config.OrgsField.FieldName),
		v.GetString(config.SubdomainField.FieldName),
		v.GetString(config.EmailField.FieldName),
		v.GetString(config.ApiTokenField.FieldName),
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
