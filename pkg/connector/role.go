package connector

import (
	"context"
	"fmt"
	"strconv"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/nukosuke/go-zendesk/zendesk"
	"go.uber.org/zap"
)

type roleResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
	connector    *Connector
}

func (r *roleResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return r.resourceType
}

// List returns all the roles from the database as resource objects.
// Roles include a RoleTrait because they are the 'shape' of a standard group.
func (r *roleResourceType) List(ctx context.Context, parentId *v2.ResourceId, _ rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var rv []*v2.Resource
	customRole, err := r.client.GetCustomRoles(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, role := range customRole {
		roleCopy := role
		rr, err := getRoleResource(&roleCopy, resourceTypeRole, parentId)
		if err != nil {
			return nil, nil, err
		}

		rv = append(rv, rr)
	}

	return rv, nil, nil
}

func (r *roleResourceType) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

func (r *roleResourceType) StaticEntitlements(_ context.Context, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	var rv []*v2.Entitlement
	for _, roleType := range []int{zendesk.UserRoleAgent, zendesk.UserRoleAdmin} {
		supportRole := zendesk.UserRoleText(roleType)
		rv = append(rv, ent.NewPermissionEntitlement(nil, supportRole,
			ent.WithGrantableTo(resourceTypeTeam, resourceTypeGroup),
		))
	}
	return rv, nil, nil
}

func (r *roleResourceType) Grants(ctx context.Context, resource *v2.Resource, opts rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	var (
		err error
		rv  []*v2.Grant
	)

	users, err := r.connector.cacheUsers(ctx, opts.Session)
	if err != nil {
		return nil, nil, err
	}

	for _, user := range users {
		userCopy := user

		resourceId, err := strconv.ParseInt(resource.Id.Resource, 10, 64)
		if err != nil {
			return nil, nil, err
		}

		if user.CustomRoleID != resourceId {
			continue
		}

		ur, err := getUserRoleResource(&userCopy, resourceTypeTeam)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating team_member resource for role %s: %w", resource.Id.Resource, err)
		}

		gr := grant.NewGrant(resource, user.Role, ur.Id)
		rv = append(rv, gr)
	}

	return rv, nil, nil
}

func (r *roleResourceType) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	if principal.Id.ResourceType != resourceTypeTeam.Id {
		l.Warn(
			"baton-zendesk: only team members can be granted role membership",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, nil, fmt.Errorf("baton-zendesk: only team members can be granted role membership")
	}

	userID, err := strconv.ParseInt(principal.Id.Resource, 10, 64)
	if err != nil {
		return nil, nil, err
	}

	user, err := r.client.GetUser(ctx, userID)
	if err != nil {
		return nil, nil, err
	}

	if user.Role == "end-user" {
		l.Warn("user must be a team member",
			zap.Int64("user", user.ID),
			zap.String("user.Role", user.Role),
		)
		return nil, nil, fmt.Errorf("user must be a team member")
	}

	roleID, err := strconv.ParseInt(entitlement.Resource.Id.Resource, 10, 64)
	if err != nil {
		return nil, nil, err
	}

	roleMembershipOptions := zendesk.CustomRole{
		Name: fmt.Sprintf("Custom Role %d ", roleID),
	}
	membership, err := r.client.CreateCustomRoleMembership(ctx, roleMembershipOptions)
	if err != nil {
		return nil, nil, fmt.Errorf("zendesk-connector: failed to add team member to a group: %s", err.Error())
	}

	l.Warn("Role Membership has been created.",
		zap.Int64("ID", membership.ID),
		zap.String("Name", membership.Name),
		zap.String("Configuration", fmt.Sprintf("%v", membership.Configuration)),
		zap.Time("CreatedAt", membership.CreatedAt),
	)

	return nil, nil, nil
}

func (r *roleResourceType) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	return nil, nil
}

func roleBuilder(c *client.ZendeskClient, connector *Connector) *roleResourceType {
	return &roleResourceType{
		resourceType: resourceTypeRole,
		client:       c,
		connector:    connector,
	}
}
