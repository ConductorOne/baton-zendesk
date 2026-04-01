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

const (
	memberEntitlement = "member"
	adminEntitlement  = "admin"
)

type groupResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
	connector    *Connector
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
func (g *groupResourceType) List(ctx context.Context, parentId *v2.ResourceId, opts rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var (
		err error
		ret []*v2.Resource
	)

	groups, nextPageToken, err := g.client.ListGroups(ctx, opts.PageToken.Token)
	if err != nil {
		return nil, nil, err
	}

	for _, group := range groups {
		res, err := getGroupResource(group, resourceTypeGroup, parentId)
		if err != nil {
			return nil, nil, err
		}

		ret = append(ret, res)
	}

	return ret, &rs.SyncOpResults{NextPageToken: nextPageToken}, nil
}

func (g *groupResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
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

	return rv, nil, nil
}

func (g *groupResourceType) Grants(ctx context.Context, resource *v2.Resource, opts rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	var rv []*v2.Grant
	groupId, err := strconv.Atoi(resource.Id.Resource)
	if err != nil {
		return nil, nil, err
	}

	// TODO: luisina - review cache handling
	users, err := g.connector.cacheUsers(ctx, opts.Session)
	mapUsers := make(map[int64]zendesk.User)
	for _, user := range users {
		mapUsers[user.ID] = user
	}
	if err != nil {
		return nil, nil, err
	}

	groupMemberships, nextPageToken, err := g.client.GetGroupMemberships(ctx, int64(groupId), opts.PageToken.Token)
	if err != nil {
		return nil, nil, err
	}

	for _, group := range groupMemberships {
		userAccountDetail := getUserByID(group.UserID, mapUsers)
		ur, err := getUserResource(userAccountDetail, resourceTypeTeam)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating team_member resource for group %s: %w", resource.Id.Resource, err)
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

	return rv, &rs.SyncOpResults{NextPageToken: nextPageToken}, nil
}

func (g *groupResourceType) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)
	if principal.Id.ResourceType != resourceTypeTeam.Id {
		l.Warn(
			"zendesk-connector: only team members can be granted group membership",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, nil, fmt.Errorf("zendesk-connector: only users can be granted team membership")
	}

	userID, err := strconv.ParseInt(principal.Id.Resource, 10, 64)
	if err != nil {
		return nil, nil, err
	}

	user, err := g.client.GetUser(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if user.Role == "end-user" {
		l.Warn("user must be a team member",
			zap.Int64("UserID", user.ID),
			zap.String("user.Role", user.Role),
		)
		return nil, nil, fmt.Errorf("user must be a team member")
	}

	groupID, err := strconv.ParseInt(entitlement.Resource.Id.Resource, 10, 64)
	if err != nil {
		return nil, nil, err
	}

	groupMembershipOptions := zendesk.GroupMembership{
		UserID:  userID,
		GroupID: groupID,
	}
	membership, err := g.client.CreateGroupMembership(ctx, groupMembershipOptions)
	if err != nil {
		return nil, nil, fmt.Errorf("zendesk-connector: failed to add team member to a group: %s", err.Error())
	}

	l.Warn("Membership has been created.",
		zap.Int64("ID", membership.ID),
		zap.Int64("UserID", membership.UserID),
		zap.Int64("GroupID", membership.GroupID),
		zap.Time("CreatedAt", membership.CreatedAt),
	)

	return nil, nil, nil
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

func groupBuilder(cli *client.ZendeskClient, con *Connector) *groupResourceType {
	return &groupResourceType{
		resourceType: resourceTypeGroup,
		client:       cli,
		connector:    con,
	}
}
