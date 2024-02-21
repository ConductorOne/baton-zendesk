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

const (
	memberEntitlement = "member"
	adminEntitlement  = "admin"
)

type groupResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
}

var groupEntitlementAccessLevels = []string{
	memberEntitlement,
	adminEntitlement,
}

func (g *groupResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return g.resourceType
}

// List returns all the groups from the database as resource objects.
// Groups include a GroupTrait because they are the 'shape' of a standard group.
func (g *groupResourceType) List(ctx context.Context, parentId *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	var (
		pageToken int
		err       error
		ret       []*v2.Resource
	)
	if pToken.Token != "" {
		pageToken, err = strconv.Atoi(pToken.Token)
		if err != nil {
			return nil, "", nil, err
		}
	}

	groups, nextPageToken, err := g.client.ListGroups(ctx, pageToken)
	if err != nil {
		return nil, "", nil, err
	}

	for _, group := range groups {
		res, err := getGroupResource(group, resourceTypeGroup, parentId)
		if err != nil {
			return nil, "", nil, err
		}

		ret = append(ret, res)
	}

	return ret, nextPageToken, nil, nil
}

func (g *groupResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement
	for _, level := range groupEntitlementAccessLevels {
		rv = append(rv, ent.NewPermissionEntitlement(resource, level,
			ent.WithDisplayName(fmt.Sprintf("%s Group %s", resource.DisplayName, titleCase(level))),
			ent.WithDescription(fmt.Sprintf("Access to %s group in Zendesk", resource.DisplayName)),
			ent.WithAnnotation(&v2.V1Identifier{
				Id: fmt.Sprintf("group:%s:role:%s", resource.Id.Resource, level),
			}),
			ent.WithGrantableTo(resourceTypeTeam),
		))
	}

	return rv, "", nil, nil
}

func (g *groupResourceType) Grants(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var rv []*v2.Grant
	groupId, err := strconv.Atoi(resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	opts := zendesk.UserListOptions{
		Roles: []string{
			"admin",
			"agent",
		},
	}

	users, _, err := g.client.GetUsers(ctx, &opts)
	if err != nil {
		return nil, "", nil, err
	}

	groupMemberships, nextPageToken, err := g.client.GetGroupMemberships(ctx, int64(groupId))
	if err != nil {
		return nil, "", nil, err
	}

	for _, group := range groupMemberships {
		userAccountDetail := getUserByID(group.UserID, users)
		ur, err := getUserResource(userAccountDetail, resourceTypeTeam)
		if err != nil {
			return nil, "", nil, fmt.Errorf("error creating team_member resource for group %s: %w", resource.Id.Resource, err)
		}

		if userAccountDetail.Role == adminEntitlement {
			adminsGrant := grant.NewGrant(resource, adminEntitlement, ur.Id)
			teamAdminsGrant := grant.NewGrant(ur, adminEntitlement, resource.Id)
			rv = append(rv, adminsGrant, teamAdminsGrant)
		}

		membershipGrant := grant.NewGrant(resource, memberEntitlement, ur.Id)
		teamMembershipGrant := grant.NewGrant(ur, memberEntitlement, resource.Id)
		rv = append(rv, membershipGrant, teamMembershipGrant)
	}

	return rv, nextPageToken, nil, nil
}

func (g *groupResourceType) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) (annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)
	if principal.Id.ResourceType != resourceTypeTeam.Id {
		l.Warn(
			"zendesk-connector: only team members can be granted group membership",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, fmt.Errorf("zendesk-connector: only users can be granted team membership")
	}

	userID, err := strconv.ParseInt(principal.Id.Resource, 10, 64)
	if err != nil {
		return nil, err
	}

	user, err := g.client.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.Role == "end-user" {
		l.Warn("user must be a team member",
			zap.String("user", fmt.Sprintf("%d", user.ID)),
			zap.String("user.Role", user.Role),
		)
		return nil, fmt.Errorf("user must be a team member")
	}

	groupID, err := strconv.ParseInt(entitlement.Resource.Id.Resource, 10, 64)
	if err != nil {
		return nil, err
	}

	groupMembershipOptions := zendesk.GroupMembership{
		UserID:  userID,
		GroupID: groupID,
	}
	membership, err := g.client.CreateGroupMembership(ctx, groupMembershipOptions)
	if err != nil {
		return nil, fmt.Errorf("zendesk-connector: failed to add team member to a group: %s", err.Error())
	}

	l.Warn("Membership has been created.",
		zap.Int64("ID", membership.ID),
		zap.String("UserID", fmt.Sprintf("%d", membership.UserID)),
		zap.String("GroupID", fmt.Sprintf("%d", membership.GroupID)),
		zap.String("CreatedAt", membership.CreatedAt.String()),
	)

	return nil, nil
}

func (g *groupResourceType) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	entitlement := grant.Entitlement
	principal := grant.Principal

	if principal.Id.ResourceType != resourceTypeTeam.Id {
		l.Warn(
			"zendesk-connector: only team members can have group membership revoked",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, fmt.Errorf("zendesk-connector: only team members can have group membership revoked")
	}

	userID, err := strconv.ParseInt(principal.Id.Resource, 10, 64)
	if err != nil {
		return nil, err
	}

	groupID, err := strconv.ParseInt(entitlement.Resource.Id.Resource, 10, 64)
	if err != nil {
		return nil, err
	}

	groupMembershipOptions := zendesk.GroupMembership{
		UserID:  userID,
		GroupID: groupID,
	}
	groupMembershipID, err := g.client.RemoveGroupMembershipByID(ctx, groupMembershipOptions)
	if err != nil {
		return nil, fmt.Errorf("zendesk-connector: failed to revoke team member: %s", err.Error())
	}

	l.Warn("Membership has been revoked..",
		zap.String("groupMembershipID", groupMembershipID),
	)

	return nil, nil
}

func groupBuilder(c *client.ZendeskClient) *groupResourceType {
	return &groupResourceType{
		resourceType: resourceTypeGroup,
		client:       c,
	}
}
