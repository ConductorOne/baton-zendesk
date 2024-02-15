package connector

import (
	"context"
	"fmt"
	"strconv"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"

	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-zendesk/pkg/client"
)

const (
	teamRoleMember      = "member"
	teamRoleAdmin       = "admin"
	teamRoleAgent       = "agent"
	teamRoleContributor = "contributor"
	teamRoleLegacyAgent = "legacy agent"
	teamRoleLightAgent  = "light agent"
	teamRoleCustomRoles = "custom roles"
)

var teamAccessLevels = []string{
	teamRoleMember,
	teamRoleAdmin,
	teamRoleAgent,
	teamRoleContributor,
	teamRoleLegacyAgent,
	teamRoleLightAgent,
	teamRoleCustomRoles,
}

type teamResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
}

func (t *teamResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return t.resourceType
}

// Entitlements always returns an empty slice for users.
func (t *teamResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	rv := make([]*v2.Entitlement, 0, len(teamAccessLevels))
	for _, level := range teamAccessLevels {
		rv = append(rv, ent.NewPermissionEntitlement(resource, level,
			ent.WithAnnotation(
				&v2.V1Identifier{
					Id: fmt.Sprintf("team_member:%s:role:%s", resource.Id.Resource, level),
				},
			),
			ent.WithDisplayName(fmt.Sprintf("%s Team Member %s", resource.DisplayName, titleCase(level))),
			ent.WithDescription(fmt.Sprintf("Access to %s team member in Zendesk", resource.DisplayName)),
			ent.WithGrantableTo(resourceTypeTeam),
		))
	}

	return rv, "", nil, nil
}

func (t *teamResourceType) List(ctx context.Context, parentID *v2.ResourceId, pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
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

	users, nextPageToken, err := t.client.ListUsers(ctx, pageToken)
	if err != nil {
		return nil, "", nil, err
	}

	for _, user := range users {
		userCopy := user
		if t.client.IsValidTeamMember(&userCopy) { // team member
			res, err := t.client.GetTeamResource(&userCopy, resourceTypeTeam, parentID)
			if err != nil {
				return nil, "", nil, err
			}

			ret = append(ret, res)
		}
	}

	return ret, nextPageToken, nil, nil
}

// Grants always returns an empty slice for teams since they don't have any entitlements.
func (o *teamResourceType) Grants(ctx context.Context, resource *v2.Resource, pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	return nil, "", nil, nil
}

func teamBuilder(c *client.ZendeskClient) *teamResourceType {
	return &teamResourceType{
		resourceType: resourceTypeTeam,
		client:       c,
	}
}
