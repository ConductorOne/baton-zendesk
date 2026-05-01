package connector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	config_sdk "github.com/conductorone/baton-sdk/pb/c1/config/v1"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/actions"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/nukosuke/go-zendesk/zendesk"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/structpb"
)

func (c *Connector) GlobalActions(ctx context.Context, registry actions.ActionRegistry) error {
	l := ctxzap.Extract(ctx)

	disableUserSchema := &v2.BatonActionSchema{
		Name:        "disable_user",
		DisplayName: "Disable User",
		Description: "Suspend a Zendesk user account by setting suspended to true",
		Arguments: []*config_sdk.Field{
			{
				Name:        userIDKey,
				DisplayName: "User ID",
				Description: "The Zendesk user ID to disable",
				IsRequired:  true,
				Field: &config_sdk.Field_StringField{
					StringField: &config_sdk.StringField{},
				},
			},
		},
		ReturnTypes: []*config_sdk.Field{
			{
				Name:        successKey,
				DisplayName: "Success",
				Field:       &config_sdk.Field_BoolField{},
			},
		},
		ActionType: []v2.ActionType{
			v2.ActionType_ACTION_TYPE_ACCOUNT,
			v2.ActionType_ACTION_TYPE_ACCOUNT_DISABLE,
		},
	}

	err := registry.Register(ctx, disableUserSchema, func(ctx context.Context, args *structpb.Struct) (*structpb.Struct, annotations.Annotations, error) {
		return c.handleDisableUser(ctx, args)
	})
	if err != nil {
		l.Error("failed to register disable_user action", zap.Error(err))
		return err
	}

	l.Info("registered disable_user action")

	enableUserSchema := &v2.BatonActionSchema{
		Name:        "enable_user",
		DisplayName: "Enable User",
		Description: "Unsuspend a Zendesk user account by setting suspended to false",
		Arguments: []*config_sdk.Field{
			{
				Name:        userIDKey,
				DisplayName: "User ID",
				Description: "The Zendesk user ID to enable",
				IsRequired:  true,
				Field: &config_sdk.Field_StringField{
					StringField: &config_sdk.StringField{},
				},
			},
		},
		ReturnTypes: []*config_sdk.Field{
			{
				Name:        successKey,
				DisplayName: "Success",
				Field:       &config_sdk.Field_BoolField{},
			},
		},
		ActionType: []v2.ActionType{
			v2.ActionType_ACTION_TYPE_ACCOUNT,
			v2.ActionType_ACTION_TYPE_ACCOUNT_ENABLE,
		},
	}

	err = registry.Register(ctx, enableUserSchema, func(ctx context.Context, args *structpb.Struct) (*structpb.Struct, annotations.Annotations, error) {
		return c.handleEnableUser(ctx, args)
	})
	if err != nil {
		l.Error("failed to register enable_user action", zap.Error(err))
		return err
	}

	l.Info("registered enable_user action")
	return nil
}

// handleDisableUser suspends a Zendesk user by setting suspended to true.
func (c *Connector) handleDisableUser(
	ctx context.Context,
	args *structpb.Struct,
) (*structpb.Struct, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	userIDValue, ok := args.Fields["user_id"]
	if !ok {
		return nil, nil, fmt.Errorf("user_id parameter is required")
	}

	userIDStr := userIDValue.GetStringValue()
	if userIDStr == "" {
		return nil, nil, fmt.Errorf("user_id cannot be empty")
	}

	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid user_id format: %w", err)
	}

	l.Debug("disabling user", zap.Int64("user_id", userID))

	// required to get the current user's name
	currentUser, err := c.zendeskClient.GetUser(ctx, userID)
	if err != nil {
		var zErr *zendesk.Error
		if ok := errors.As(err, &zErr); ok {
			body, _ := io.ReadAll(zErr.Body())
			l.Error("failed to get user", zap.Int64("user_id", userID), zap.Int("status_code", zErr.Status()), zap.String("body", string(body)))
		}
		l.Error("failed to get user", zap.Int64("user_id", userID), zap.Error(err))
		response := &structpb.Struct{
			Fields: map[string]*structpb.Value{
				successKey: structpb.NewBoolValue(false),
			},
		}
		return response, nil, err
	}

	body := map[string]any{
		userKey: map[string]any{
			"name":      currentUser.Name,
			"suspended": true,
		},
	}
	_, err = c.zendeskClient.UpdateUser(ctx, userID, body)
	if err != nil {
		var zErr *zendesk.Error
		if ok := errors.As(err, &zErr); ok {
			body, _ := io.ReadAll(zErr.Body())
			l.Error("failed to disable user", zap.Int64("user_id", userID), zap.Int("status_code", zErr.Status()), zap.String("body", string(body)))
		}
		l.Error("failed to disable user", zap.Int64("user_id", userID), zap.Error(err))
		response := &structpb.Struct{
			Fields: map[string]*structpb.Value{
				successKey: structpb.NewBoolValue(false),
			},
		}
		return response, nil, err
	}

	response := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			successKey: structpb.NewBoolValue(true),
		},
	}
	return response, nil, nil
}

// handleEnableUser unsuspends a Zendesk user by setting suspended to false.
func (c *Connector) handleEnableUser(
	ctx context.Context,
	args *structpb.Struct,
) (*structpb.Struct, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	userIDValue, ok := args.Fields["user_id"]
	if !ok {
		return nil, nil, fmt.Errorf("user_id parameter is required")
	}

	userIDStr := userIDValue.GetStringValue()
	if userIDStr == "" {
		return nil, nil, fmt.Errorf("user_id cannot be empty")
	}

	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid user_id format: %w", err)
	}

	l.Debug("enabling user", zap.Int64("user_id", userID))

	payload := map[string]any{
		userKey: map[string]any{
			"suspended": false,
		},
	}
	response, err := c.zendeskClient.UpdateUser(ctx, userID, payload)
	if err != nil {
		var zErr *zendesk.Error
		if ok := errors.As(err, &zErr); ok {
			errorBody, _ := io.ReadAll(zErr.Body())
			l.Error("failed to enable user", zap.Int64("user_id", userID), zap.Int("status_code", zErr.Status()), zap.String("body", string(errorBody)))
		}
		l.Error("failed to enable user", zap.Int64("user_id", userID), zap.Error(err))
		response := &structpb.Struct{
			Fields: map[string]*structpb.Value{
				successKey: structpb.NewBoolValue(false),
			},
		}
		return response, nil, err
	}

	l.Info("user enabled successfully", zap.Int64("user_id", userID), zap.Bool("suspended", response.Suspended))

	return &structpb.Struct{
		Fields: map[string]*structpb.Value{
			successKey: structpb.NewBoolValue(true),
		},
	}, nil, nil
}
