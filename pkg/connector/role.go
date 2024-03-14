package connector

import (
	"context"
	"fmt"
	"strconv"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/nukosuke/go-zendesk/zendesk"
	"go.uber.org/zap"
)

type roleResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
}

var users []zendesk.User

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
		rr, err := getRoleResource(&roleCopy, resourceTypeRole, parentId)
		if err != nil {
			return nil, "", nil, err
		}
		rv = append(rv, rr)
	}

	return rv, "", nil, nil
}

func (r *roleResourceType) Entitlements(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var (
		pageToken     int
		err           error
		rv            []*v2.Entitlement
		nextPageToken string
	)

	if token.Token != "" {
		pageToken, err = strconv.Atoi(token.Token)
		if err != nil {
			return nil, "", nil, err
		}
	}

	users, nextPageToken, err = r.client.ListUsers(ctx, pageToken)
	if err != nil {
		return nil, "", nil, err
	}

	for supportRole := range getUserSupportRoles(users) {
		permissionOptions := PopulateOptions(resource.DisplayName, supportRole, resource.Id.Resource)
		permissionEn := ent.NewPermissionEntitlement(resource, supportRole, permissionOptions...)
		rv = append(rv, permissionEn)
	}

	return rv, nextPageToken, nil, nil
}

func (r *roleResourceType) Grants(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var (
		pageToken     int
		err           error
		rv            []*v2.Grant
		nextPageToken string
	)

	if token.Token != "" {
		pageToken, err = strconv.Atoi(token.Token)
		if err != nil {
			return nil, "", nil, err
		}
	}

	if len(users) == 0 {
		users, nextPageToken, err = r.client.ListUsers(ctx, pageToken)
		if err != nil {
			return nil, "", nil, err
		}
	}

	for _, user := range users {
		userCopy := user
		if !isValidTeamMember(&userCopy) {
			continue
		}

		resourceId, err := strconv.ParseInt(resource.Id.Resource, 10, 64)
		if err != nil {
			return nil, "", nil, err
		}

		if user.CustomRoleID != resourceId {
			continue
		}

		ur, err := getUserRoleResource(&userCopy, resourceTypeTeam)
		if err != nil {
			return nil, "", nil, fmt.Errorf("error creating team_member resource for role %s: %w", resource.Id.Resource, err)
		}

		gr := grant.NewGrant(resource, user.Role, ur.Id)
		tr := grant.NewGrant(ur, user.Role, resource.Id)
		rv = append(rv, gr, tr)
	}

	return rv, nextPageToken, nil, nil
}

func (r *roleResourceType) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	if principal.Id.ResourceType != resourceTypeTeam.Id {
		l.Warn(
			"baton-zendesk: only team members can be granted role membership",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, fmt.Errorf("baton-zendesk: only team members can be granted role membership")
	}

	userID, err := strconv.ParseInt(principal.Id.Resource, 10, 64)
	if err != nil {
		return nil, err
	}

	user, err := r.client.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	if user.Role == "end-user" {
		l.Warn("user must be a team member",
			zap.Int64("user", user.ID),
			zap.String("user.Role", user.Role),
		)
		return nil, fmt.Errorf("user must be a team member")
	}

	roleID, err := strconv.ParseInt(entitlement.Resource.Id.Resource, 10, 64)
	if err != nil {
		return nil, err
	}

	roleMembershipOptions := zendesk.CustomRole{
		Name: fmt.Sprintf("Custom Role %d ", roleID),
	}
	membership, err := r.client.CreateCustomRoleMembership(ctx, roleMembershipOptions)
	if err != nil {
		return nil, fmt.Errorf("zendesk-connector: failed to add team member to a group: %s", err.Error())
	}

	l.Warn("Role Membership has been created.",
		zap.Int64("ID", membership.ID),
		zap.String("Name", membership.Name),
		zap.String("Configuration", fmt.Sprintf("%v", membership.Configuration)),
		zap.Time("CreatedAt", membership.CreatedAt),
	)

	return nil, nil
}

func (r *roleResourceType) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	return nil, nil
}

func roleBuilder(c *client.ZendeskClient) *roleResourceType {
	return &roleResourceType{
		resourceType: resourceTypeRole,
		client:       c,
	}
}
