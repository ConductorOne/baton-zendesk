package connector

import (
	"context"
	"fmt"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/nukosuke/go-zendesk/zendesk"
)

type roleResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
}

func (r *roleResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return r.resourceType
}

// List returns all the roles from the database as resource objects.
// Roles include a RoleTrait because they are the 'shape' of a standard group.
func (r *roleResourceType) List(ctx context.Context, parentId *v2.ResourceId, token *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var rv []*v2.Resource
	customRole, err := r.client.GetCustomRoles(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	for _, role := range customRole {
		roleCopy := role
		rr, err := r.client.GetRoleResource(&roleCopy, resourceTypeRole, parentId)
		if err != nil {
			return nil, "", nil, err
		}
		rv = append(rv, rr)
	}

	return rv, "", nil, nil
}

func (r *roleResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement

	assigmentOptions := PopulateOptions(resource.DisplayName, memberEntitlement, resource.Id.Resource)
	assignmentEn := ent.NewAssignmentEntitlement(resource, memberEntitlement, assigmentOptions...)

	permissionOptions := PopulateOptions(resource.DisplayName, adminEntitlement, resource.Id.Resource)
	permissionEn := ent.NewPermissionEntitlement(resource, adminEntitlement, permissionOptions...)

	rv = append(rv, assignmentEn, permissionEn)

	return rv, "", nil, nil
}

func (r *roleResourceType) Grants(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var rv []*v2.Grant
	userAccounts, groups, nextPageToken, err := r.GetAccounts(ctx)
	if err != nil {
		return nil, "", nil, err
	}

	customRoles, err := r.client.GetCustomRoles(ctx)
	if err != nil {
		return nil, "", nil, err
	}

	for _, group := range groups {
		groupCopy := group
		gr, err := r.client.GetGroupResource(groupCopy, resourceTypeGroup, resource.Id)
		if err != nil {
			return nil, "", nil, err
		}

		for _, customRole := range customRoles {
			if resource.Id.Resource == customRole.Name {
				rv = append(rv, grant.NewGrant(resource, memberEntitlement, gr.Id, grant.WithAnnotation(&v2.V1Identifier{
					Id: fmt.Sprintf("role-grant:%s:%d:%s", resource.Id.Resource, group.ID, customRole.Name),
				})))
			}
		}
	}

	for _, userAccount := range userAccounts {
		userAccountCopy := userAccount
		gr, err := r.client.GetUserAccountResource(&userAccountCopy, resourceTypeTeam, resource.Id)
		if err != nil {
			return nil, "", nil, err
		}

		if resource.Id.Resource == userAccount.Role {
			rv = append(rv, grant.NewGrant(resource, memberEntitlement, gr.Id, grant.WithAnnotation(&v2.V1Identifier{
				Id: fmt.Sprintf("role-grant:%s:%d:%s", resource.Id.Resource, userAccount.ID, userAccount.Role),
			})))
		}
	}

	return rv, nextPageToken, nil, nil
}

func (r *roleResourceType) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	return nil, nil
}

func (r *roleResourceType) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	return nil, nil
}

// GetAccounts returns all zendesk users and groups.
func (r *roleResourceType) GetAccounts(ctx context.Context) ([]zendesk.User, []zendesk.Group, string, error) {
	var (
		userAccounts   []zendesk.User
		groupsAccounts []zendesk.Group
	)
	users, nextPageToken, err := r.client.ListUsers(ctx, 0)
	if err != nil {
		return nil, nil, "", err
	}

	groups, _, err := r.client.ListGroups(ctx, 0)
	if err != nil {
		return nil, nil, "", err
	}

	for _, user := range users {
		userAccountInfo, err := r.client.GetUser(ctx, user.ID)
		if err != nil {
			return nil, nil, "", err
		}
		userAccounts = append(userAccounts, userAccountInfo)
	}

	for _, group := range groups {
		groupInfo, err := r.client.GetGroupDetails(ctx, group.ID)
		if err != nil {
			return nil, nil, "", err
		}
		groupsAccounts = append(groupsAccounts, groupInfo)
	}

	return userAccounts, groupsAccounts, nextPageToken, nil
}

func roleBuilder(c *client.ZendeskClient) *roleResourceType {
	return &roleResourceType{
		resourceType: resourceTypeRole,
		client:       c,
	}
}
