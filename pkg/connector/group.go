package connector

import (
	"context"
	"strconv"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/nukosuke/go-zendesk/zendesk"
)

const (
	memberEntitlement = "member"
	adminEntitlement  = "admin"
)

type groupResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
}

func (g *groupResourceType) ResourceType(ctx context.Context) *v2.ResourceType {
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
		res, err := g.groupResource(group, parentId)
		if err != nil {
			return nil, "", nil, err
		}

		ret = append(ret, res)
	}

	return ret, nextPageToken, nil, nil
}

func (g *groupResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	var rv []*v2.Entitlement

	assigmentOptions := PopulateOptions(resource.DisplayName, memberEntitlement, resource.Id.Resource)
	assignmentEn := ent.NewAssignmentEntitlement(resource, memberEntitlement, assigmentOptions...)

	permissionOptions := PopulateOptions(resource.DisplayName, adminEntitlement, resource.Id.Resource)
	permissionEn := ent.NewPermissionEntitlement(resource, adminEntitlement, permissionOptions...)

	rv = append(rv, assignmentEn, permissionEn)

	return rv, "", nil, nil
}

func (g *groupResourceType) Grants(ctx context.Context, resource *v2.Resource, token *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var rv []*v2.Grant
	groupId, err := strconv.Atoi(resource.Id.Resource)
	if err != nil {
		return nil, "", nil, err
	}

	groupMemberships, nextPageToken, err := g.client.GetGroupMemberships(ctx, int64(groupId))
	if err != nil {
		return nil, "", nil, err
	}

	for _, user := range groupMemberships {
		userAccountDetail, err := g.client.GetUser(ctx, user.UserID)
		if err != nil {
			return nil, "", nil, err
		}

		ur, err := g.userAccountResource(userAccountDetail)
		if err != nil {
			return nil, "", nil, err
		}

		membershipGrant := grant.NewGrant(resource, memberEntitlement, ur.Id)
		rv = append(rv, membershipGrant)

		if userAccountDetail.Role == adminEntitlement {
			adminsGrant := grant.NewGrant(resource, adminEntitlement, ur.Id)
			rv = append(rv, adminsGrant)
		}
	}

	return rv, nextPageToken, nil, nil
}

func groupBuilder(c *client.ZendeskClient) *groupResourceType {
	return &groupResourceType{
		resourceType: resourceTypeGroup,
		client:       c,
	}
}

// Create a new connector resource for a Zenddesk group.
func (o *groupResourceType) groupResource(group zendesk.Group, parentResourceID *v2.ResourceId) (*v2.Resource, error) {
	profile := map[string]interface{}{
		"group_id":   group.ID,
		"group_name": group.Name,
	}
	groupTraitOptions := []rs.GroupTraitOption{rs.WithGroupProfile(profile)}
	ret, err := rs.NewGroupResource(
		group.Name,
		resourceTypeGroup,
		group.ID,
		groupTraitOptions,
		rs.WithParentResourceID(parentResourceID),
	)
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (o *groupResourceType) userAccountResource(user zendesk.User) (*v2.Resource, error) {
	resource, err := rs.NewUserResource(user.Name, resourceTypeUser, user.ID, nil)
	if err != nil {
		return nil, err
	}

	return resource, nil
}
