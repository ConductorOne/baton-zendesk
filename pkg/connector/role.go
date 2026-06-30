package connector

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
		if isSupportProductInactiveError(err) {
			ctxzap.Extract(ctx).Debug("baton-zendesk: custom roles API unavailable (SupportProductInactive); skipping custom roles")
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("baton-zendesk: failed to list custom roles: %w", err)
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
	roleID, err := strconv.ParseInt(resource.Id.Resource, 10, 64)
	if err != nil {
		return nil, nil, err
	}

	users, nextPageToken, err := r.client.ListUsersByRole(ctx, roleID, opts.PageToken.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to list users by role %d: %w", roleID, err)
	}

	var rv []*v2.Grant
	for _, user := range users {
		userCopy := user
		ur, err := getUserRoleResource(&userCopy, resourceTypeTeam)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating team_member resource for role %s: %w", resource.Id.Resource, err)
		}
		rv = append(rv, grant.NewGrant(resource, strings.ToLower(user.Role), ur.Id))
	}

	return rv, &rs.SyncOpResults{NextPageToken: nextPageToken}, nil
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

	roleID, err := strconv.ParseInt(entitlement.Resource.Id.Resource, 10, 64)
	if err != nil {
		return nil, nil, err
	}

	// Role assignment via PUT is naturally idempotent: re-assigning the same custom role
	// returns 200 and is a harmless no-op, so no pre-GET / GrantAlreadyExists is needed.
	updatedUser, err := r.client.UpdateUser(ctx, userID, map[string]any{userKey: map[string]any{"custom_role_id": roleID}})
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to assign custom role to user: %w", err)
	}

	l.Debug("Custom role assigned to user.",
		zap.Int64("userID", updatedUser.ID),
		zap.Int64("roleID", roleID),
	)

	return nil, nil, nil
}

func (r *roleResourceType) Revoke(ctx context.Context, g *v2.Grant) (annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	principal := g.Principal
	entitlement := g.Entitlement

	if principal.Id.ResourceType != resourceTypeTeam.Id {
		return nil, fmt.Errorf("baton-zendesk: only team members can have role revoked, got %q",
			principal.Id.ResourceType)
	}

	// Built-in role entitlements (from StaticEntitlements) have no concrete resource ID.
	// Revoking would require a downgrade target which isn't modeled here.
	if entitlement.Resource.Id.Resource == "" {
		return nil, fmt.Errorf("baton-zendesk: revoking built-in role entitlement %q is not supported; "+
			"manage role downgrades by assigning a lower role instead", entitlement.Slug)
	}

	userID, err := strconv.ParseInt(principal.Id.Resource, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("baton-zendesk: invalid principal id: %w", err)
	}

	// Clearing the custom role via PUT is idempotent: if the user already has no custom role
	// (or it was already cleared), Zendesk returns 200 and the field stays null. A deleted user
	// surfaces as 404 on the PUT below, so no pre-GET is needed.
	//
	// Verified live (CXH-1284 probe): Zendesk rejects custom_role_id=0 with HTTP 400
	// "Invalid custom role id"; only JSON null clears the field. Use a raw map with a
	// nil value so encoding/json emits null (the typed UpdateUser would strip via omitempty).
	if _, err := r.client.UpdateUser(ctx, userID, map[string]any{
		userKey: map[string]any{"custom_role_id": nil},
	}); err != nil {
		if isNotFoundError(err) {
			annos := annotations.New()
			annos.Update(&v2.GrantAlreadyRevoked{})
			return annos, nil
		}
		return nil, fmt.Errorf("baton-zendesk: failed to revoke custom role from user %d: %w", userID, err)
	}

	l.Debug("baton-zendesk: custom role revoked",
		zap.Int64("user_id", userID),
		zap.String("role_id", entitlement.Resource.Id.Resource),
	)
	return nil, nil
}

func roleBuilder(c *client.ZendeskClient) *roleResourceType {
	return &roleResourceType{
		resourceType: resourceTypeRole,
		client:       c,
	}
}
