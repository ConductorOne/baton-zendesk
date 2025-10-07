package connector

import (
	"context"
	"fmt"
	"strconv"

	config_sdk "github.com/conductorone/baton-sdk/pb/c1/config/v1"
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/actions"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/structpb"
)

func (c *Connector) RegisterActionManager(ctx context.Context) (connectorbuilder.CustomActionManager, error) {
	l := ctxzap.Extract(ctx)

	actionManager := actions.NewActionManager(ctx)

	disableUserSchema := &v2.BatonActionSchema{
		Name:        "disable_user",
		DisplayName: "Disable User",
		Description: "Suspend a Zendesk user account by setting suspended to true",
		Arguments: []*config_sdk.Field{
			{
				Name:        "user_id",
				DisplayName: "User ID",
				Description: "The Zendesk user ID to disable",
				IsRequired:  true,
				Field: &config_sdk.Field_StringField{
					StringField: &config_sdk.StringField{},
				},
			},
		},
		ReturnTypes: []*config_sdk.Field{},
	}

	err := actionManager.RegisterAction(ctx, "disable_user", disableUserSchema, func(ctx context.Context, args *structpb.Struct) (*structpb.Struct, annotations.Annotations, error) {
		return c.handleDisableUser(ctx, args)
	})
	if err != nil {
		l.Error("failed to register disable_user action", zap.Error(err))
		return nil, err
	}

	l.Info("registered disable_user action")

	enableUserSchema := &v2.BatonActionSchema{
		Name:        "enable_user",
		DisplayName: "Enable User",
		Description: "Unsuspend a Zendesk user account by setting suspended to false",
		Arguments: []*config_sdk.Field{
			{
				Name:        "user_id",
				DisplayName: "User ID",
				Description: "The Zendesk user ID to enable",
				IsRequired:  true,
				Field: &config_sdk.Field_StringField{
					StringField: &config_sdk.StringField{},
				},
			},
		},
		ReturnTypes: []*config_sdk.Field{},
	}

	err = actionManager.RegisterAction(ctx, "enable_user", enableUserSchema, func(ctx context.Context, args *structpb.Struct) (*structpb.Struct, annotations.Annotations, error) {
		return c.handleEnableUser(ctx, args)
	})
	if err != nil {
		l.Error("failed to register enable_user action", zap.Error(err))
		return nil, err
	}

	l.Info("registered enable_user action")
	return actionManager, nil
}

// handleDisableUser suspends a Zendesk user by setting suspended to true.
//
// Allowed for: Admins or agents with permission to edit end-user profiles
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/users/users/#update-user
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

	currentUser, err := c.zendeskClient.GetUser(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get user: %w", err)
	}

	currentUser.Suspended = true

	updatedUser, err := c.zendeskClient.UpdateUser(ctx, userID, currentUser)
	if err != nil {
		return &structpb.Struct{
			Fields: map[string]*structpb.Value{
				"success": {Kind: &structpb.Value_BoolValue{BoolValue: false}},
				"message": {Kind: &structpb.Value_StringValue{StringValue: fmt.Sprintf("Failed to disable user: %v", err)}},
			},
		}, nil, err
	}

	l.Info("user disabled successfully",
		zap.Int64("user_id", userID),
		zap.Bool("suspended", updatedUser.Suspended))

	return &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"success":   {Kind: &structpb.Value_BoolValue{BoolValue: true}},
			"message":   {Kind: &structpb.Value_StringValue{StringValue: fmt.Sprintf("User %d disabled successfully", userID)}},
			"user_id":   {Kind: &structpb.Value_StringValue{StringValue: fmt.Sprintf("%d", updatedUser.ID)}},
			"suspended": {Kind: &structpb.Value_BoolValue{BoolValue: updatedUser.Suspended}},
		},
	}, nil, nil
}

// handleEnableUser unsuspends a Zendesk user by setting suspended to false.
//
// Allowed for: Admins or agents with permission to edit end-user profiles
//
// Zendesk API docs: https://developer.zendesk.com/api-reference/ticketing/users/users/#update-user
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

	currentUser, err := c.zendeskClient.GetUser(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get user: %w", err)
	}

	currentUser.Suspended = false

	updatedUser, err := c.zendeskClient.UpdateUser(ctx, userID, currentUser)
	if err != nil {
		return &structpb.Struct{
			Fields: map[string]*structpb.Value{
				"success": {Kind: &structpb.Value_BoolValue{BoolValue: false}},
				"message": {Kind: &structpb.Value_StringValue{StringValue: fmt.Sprintf("Failed to enable user: %v", err)}},
			},
		}, nil, err
	}
	l.Debug("user enabled", zap.Int64("user_id", userID), zap.Bool("suspended", updatedUser.Suspended))

	l.Info("user enabled successfully",
		zap.Int64("user_id", userID),
		zap.Bool("suspended", updatedUser.Suspended))

	return &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"success":   {Kind: &structpb.Value_BoolValue{BoolValue: true}},
			"message":   {Kind: &structpb.Value_StringValue{StringValue: fmt.Sprintf("User %d enabled successfully", userID)}},
			"user_id":   {Kind: &structpb.Value_StringValue{StringValue: fmt.Sprintf("%d", updatedUser.ID)}},
			"suspended": {Kind: &structpb.Value_BoolValue{BoolValue: updatedUser.Suspended}},
		},
	}, nil, nil
}
