package connector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/nukosuke/go-zendesk/zendesk"
)

type teamMemberResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
}

func (t *teamMemberResourceType) ResourceType(ctx context.Context) *v2.ResourceType {
	return t.resourceType
}

// Team Members are users with the role of "agent" or "admin". users with the role of "end-user" are not team members, but rather customers.
func (t *teamMemberResourceType) List(ctx context.Context, parentResourceID *v2.ResourceId, opts rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	users, nextPageToken, err := t.client.ListUsers(ctx, opts.PageToken.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to list users: %w", err)
	}

	if err := populateCache(ctx, opts.Session, users); err != nil {
		return nil, nil, err
	}

	var rv []*v2.Resource
	for _, user := range users {
		userCopy := user
		userResource, err := getTeamResource(&userCopy, t.resourceType)
		if err != nil {
			return nil, nil, err
		}
		rv = append(rv, userResource)
	}

	return rv, &rs.SyncOpResults{NextPageToken: nextPageToken}, nil
}

func (t *teamMemberResourceType) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func (t *teamMemberResourceType) Grants(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func (t *teamMemberResourceType) CreateAccountCapabilityDetails(ctx context.Context) (*v2.CredentialDetailsAccountProvisioning, annotations.Annotations, error) {
	return &v2.CredentialDetailsAccountProvisioning{
		SupportedCredentialOptions: []v2.CapabilityDetailCredentialOption{
			v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
		},
		PreferredCredentialOption: v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
	}, nil, nil
}

func (t *teamMemberResourceType) CreateAccount(
	ctx context.Context,
	accountInfo *v2.AccountInfo,
	credentialOptions *v2.LocalCredentialOptions,
) (connectorbuilder.CreateAccountResponse, []*v2.PlaintextData, annotations.Annotations, error) {
	pMap := accountInfo.Profile.AsMap()

	name, ok := pMap["name"].(string)
	if !ok || name == "" {
		return nil, nil, nil, fmt.Errorf("name not found in profile")
	}

	newUser := zendesk.User{
		Name: name,
		// role can be "admin", "agent", or "end-user"
		Role: "agent",
	}

	if email, ok := pMap["email"].(string); ok && email != "" {
		newUser.Email = email
	}

	if role, ok := pMap["role"].(string); ok && role != "" {
		newUser.Role = role
	}

	createdUser, err := t.client.CreateUser(ctx, newUser)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create user: %w", err)
	}

	userResource, err := getTeamResource(&createdUser, t.resourceType)
	if err != nil {
		return nil, nil, nil, err
	}

	return &v2.CreateAccountResponse_SuccessResult{
		Resource:              userResource,
		IsCreateAccountResult: true,
	}, nil, nil, nil
}

func (t *teamMemberResourceType) Delete(ctx context.Context, resourceId *v2.ResourceId) (annotations.Annotations, error) {
	userID, err := strconv.ParseInt(resourceId.Resource, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse user ID: %w", err)
	}

	// DeleteUser will return an error when successful (yes, really)
	// see: vendor/github.com/nukosuke/go-zendesk/zendesk/zendesk.go Line:279
	err = t.client.DeleteUser(ctx, userID)
	if err != nil {
		var zErr *zendesk.Error
		if errors.As(err, &zErr) {
			if zErr.Status() == http.StatusUnprocessableEntity {
				return nil, fmt.Errorf("failed to soft delete user %s: %w", resourceId.Resource, err)
			}
		}
	}

	// PermanentlyDeleteUser also returns an error on success
	err = t.client.PermanentlyDeleteUser(ctx, userID)
	if err != nil {
		var zErr *zendesk.Error
		if errors.As(err, &zErr) {
			if zErr.Status() == http.StatusUnprocessableEntity {
				return nil, fmt.Errorf("user %s is not in the deleted state, cannot permanently delete: %w", resourceId.Resource, err)
			}
		}
	}

	return nil, nil
}

func teamMemberBuilder(zendeskClient *client.ZendeskClient) *teamMemberResourceType {
	return &teamMemberResourceType{
		resourceType: resourceTypeTeam,
		client:       zendeskClient,
	}
}
