package connector

import (
	"context"
	"fmt"
	"strconv"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/pagination"

	"github.com/conductorone/baton-zendesk/pkg/client"
)

type teamResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
	connector    *Connector
}

func (t *teamResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return t.resourceType
}

func (t *teamResourceType) Entitlements(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func (t *teamResourceType) List(ctx context.Context, parentID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var (
		err error
		ret []*v2.Resource
	)

	users, err := t.connector.cacheUsers(ctx)
	if err != nil {
		return nil, "", nil, err
	}

	for _, user := range users {
		userCopy := user
		res, err := getTeamResource(&userCopy, resourceTypeTeam)
		if err != nil {
			return nil, "", nil, err
		}

		ret = append(ret, res)
	}

	return ret, "", nil, nil
}

// Grants always returns an empty slice for teams since they don't have any entitlements.
func (o *teamResourceType) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func (t *teamResourceType) CreateAccount(
	ctx context.Context,
	accountInfo *v2.AccountInfo,
	credentialOptions *v2.LocalCredentialOptions,
) (
	connectorbuilder.CreateAccountResponse,
	[]*v2.PlaintextData,
	annotations.Annotations,
	error,
) {
	outputAnnotations := annotations.New()

	profile := accountInfo.Profile.AsMap()
	name, ok := profile["name"].(string)
	if !ok || name == "" {
		return nil, nil, outputAnnotations, fmt.Errorf("name is required for user creation")
	}

	email, ok := profile["email"].(string)
	if !ok || email == "" {
		return nil, nil, outputAnnotations, fmt.Errorf("email is required for user creation")
	}

	role, _ := profile["role"].(string)
	createdUser, err := t.client.CreateUser(ctx, name, email, role)
	if err != nil {
		return nil, nil, outputAnnotations, fmt.Errorf("failed to create user: %w", err)
	}

	resource, err := getTeamResource(createdUser, resourceTypeTeam)
	if err != nil {
		return nil, nil, outputAnnotations, fmt.Errorf("failed to create team resource: %w", err)
	}

	return &v2.CreateAccountResponse_SuccessResult{
		Resource: resource,
	}, nil, outputAnnotations, nil
}

func (t *teamResourceType) Delete(ctx context.Context, resourceId *v2.ResourceId) (annotations.Annotations, error) {
	outputAnnotations := annotations.New()

	userID, err := strconv.ParseInt(resourceId.Resource, 10, 64)
	if err != nil {
		return outputAnnotations, fmt.Errorf("invalid user ID: %w", err)
	}

	err = t.client.DeleteUser(ctx, userID)
	if err != nil {
		return outputAnnotations, fmt.Errorf("failed to delete user: %w", err)
	}

	return outputAnnotations, nil
}

func (t *teamResourceType) CreateAccountCapabilityDetails(ctx context.Context) (*v2.CredentialDetailsAccountProvisioning, annotations.Annotations, error) {
	return &v2.CredentialDetailsAccountProvisioning{
		SupportedCredentialOptions: []v2.CapabilityDetailCredentialOption{
			v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
		},
		PreferredCredentialOption: v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
	}, nil, nil
}

func teamBuilder(cli *client.ZendeskClient, con *Connector) *teamResourceType {
	return &teamResourceType{
		resourceType: resourceTypeTeam,
		client:       cli,
		connector:    con,
	}
}
