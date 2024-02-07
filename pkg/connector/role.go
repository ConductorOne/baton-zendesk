package connector

import (
	"context"
	"fmt"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	"github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/nukosuke/go-zendesk/zendesk"
)

type roleResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
}

var privileges = []string{
	"Light agent",
	"Contributor",
	"Billing admin",
	"Admin",
}

func (o *roleResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return o.resourceType
}

// List returns all the roles from the database as resource objects.
// Roles include a RoleTrait because they are the 'shape' of a standard group.
func (o *roleResourceType) List(ctx context.Context, parentId *v2.ResourceId, token *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var rv []*v2.Resource
	for _, privilege := range privileges {
		rr, err := roleResource(ctx, privilege, parentId)
		if err != nil {
			return nil, "", nil, err
		}
		rv = append(rv, rr)
	}

	return rv, "", nil, nil
}

func (o *roleResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement

	privilegeOptions := []ent.EntitlementOption{
		ent.WithGrantableTo(resourceTypeUser, resourceTypeGroup),
		ent.WithDescription(fmt.Sprintf("Privilege set of %s", resource.DisplayName)),
		ent.WithDisplayName(fmt.Sprintf("%s privilege set %s", resource.DisplayName, memberEntitlement)),
	}

	priviledgesEn := ent.NewPermissionEntitlement(resource, memberEntitlement, privilegeOptions...)
	rv = append(rv, priviledgesEn)

	return rv, "", nil, nil
}

func (o *roleResourceType) Grants(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var rv []*v2.Grant
	userAccounts, groups, nextPageToken, err := o.GetAccounts(ctx)
	if err != nil {
		return nil, "", nil, err
	}

	customRoles, err := o.client.GetCustomRoles(ctx)
	if err != nil {
		return nil, "", nil, err
	}

	for _, group := range groups {
		groupCopy := group
		gr, err := groupResource(&groupCopy, resource.Id)
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
	// rv = append(rv, rvGroups...)
	for _, userAccount := range userAccounts {
		userAccountCopy := userAccount
		gr, err := userAccountResource(&userAccountCopy, resource.Id)
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

// GetAccounts returns all zendesk accounts.
func (c *roleResourceType) GetAccounts(ctx context.Context) ([]zendesk.User, []zendesk.Group, string, error) {
	var (
		userAccounts   []zendesk.User
		groupsAccounts []zendesk.Group
	)
	users, nextPageToken, err := c.client.ListUsers(ctx, 0)
	if err != nil {
		return nil, nil, "", err
	}

	groups, _, err := c.client.ListGroups(ctx, 0)
	if err != nil {
		return nil, nil, "", err
	}

	for _, user := range users {
		userAccountInfo, err := c.client.GetUser(ctx, user.ID)
		if err != nil {
			return nil, nil, "", err
		}
		userAccounts = append(userAccounts, userAccountInfo)
	}

	for _, group := range groups {
		groupInfo, err := c.client.GetGroupDetails(ctx, group.ID)
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

// Create a new connector resource for a zendesk group.
func groupResource(group *zendesk.Group, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"group_id":   group.ID,
		"group_name": group.Name,
	}

	groupTraitOptions := []resource.GroupTraitOption{resource.WithGroupProfile(profile)}

	ret, err := resource.NewGroupResource(
		group.Name,
		resourceTypeGroup,
		group.ID,
		groupTraitOptions,
		resource.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// Create a new connector resource for a Jamf user account.
func userAccountResource(account *zendesk.User, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	var (
		firstName, lastName string
		userStatus          v2.UserTrait_Status_Status
	)
	names := strings.SplitN(account.Name, " ", 2)

	switch len(names) {
	case 1:
		firstName = names[0]
	case 2:
		firstName = names[0]
		lastName = names[1]
	}

	profile := map[string]interface{}{
		"first_name": firstName,
		"last_name":  lastName,
		"login":      account.Email,
		"user_id":    fmt.Sprintf("account:%d", account.ID),
	}
	if account.Active {
		userStatus = v2.UserTrait_Status_STATUS_ENABLED
	} else {
		userStatus = v2.UserTrait_Status_STATUS_DISABLED
	}

	userTraitOptions := []resource.UserTraitOption{
		resource.WithUserProfile(profile),
		resource.WithEmail(account.Email, true),
		resource.WithStatus(userStatus),
	}

	ret, err := resource.NewUserResource(
		account.Name,
		resourceTypeUser,
		account.ID,
		userTraitOptions,
		resource.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

// Create a new connector resource for a Zendesk role.
func roleResource(ctx context.Context, role string, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"role_name": role,
		"role_id":   role,
	}

	roleTraitOptions := []resource.RoleTraitOption{
		resource.WithRoleProfile(profile),
	}

	ret, err := resource.NewRoleResource(
		role,
		resourceTypeRole,
		role,
		roleTraitOptions,
		resource.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}
