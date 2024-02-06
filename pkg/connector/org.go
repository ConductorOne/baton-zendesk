package connector

import (
	"context"
	"fmt"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/entitlement"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/nukosuke/go-zendesk/zendesk"
	"go.uber.org/zap"
)

type orgResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
	orgs         map[string]struct{}
}

const (
	orgRoleMember = "end-user"
	orgRoleAdmin  = "admin"
	orgRoleAgent  = "agent"
)

var orgAccessLevels = []string{
	orgRoleMember,
	orgRoleAdmin,
	orgRoleAgent,
}

func (o *orgResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return o.resourceType
}

func (o *orgResourceType) List(ctx context.Context, parentResourceID *v2.ResourceId,
	pToken *pagination.Token) ([]*v2.Resource, string, annotations.Annotations, error) {
	_, page, err := parsePageToken(pToken.Token, &v2.ResourceId{ResourceType: resourceTypeOrg.Id})
	if err != nil {
		return nil, "", nil, err
	}

	opts := &zendesk.OrganizationListOptions{
		PageOptions: zendesk.PageOptions{
			Page:    page,
			PerPage: pToken.Size,
		},
	}

	orgs, _, err := o.client.GetOrganizations(ctx, opts)
	if err != nil {
		return nil, "", nil, fmt.Errorf("zendesk-connector: failed to fetch org: %w", err)
	}
	var ret []*v2.Resource
	for _, org := range orgs {
		if _, ok := o.orgs[org.Name]; !ok && len(o.orgs) > 0 {
			continue
		}
		memberships, _, err := o.client.GetOrganizationMemberships(ctx, &zendesk.OrganizationMembershipListOptions{
			OrganizationID: org.ID,
		})
		if err != nil {
			return nil, "", nil, err
		}
		for _, membership := range memberships {
			membershipRole, _, err := o.client.GetRole(ctx, membership)
			if err != nil {
				return nil, "", nil, fmt.Errorf("zendesk-connector: failed to get role member: %w", err)
			}
			// Only sync orgs that we are an admin for
			if strings.ToLower(membershipRole) != orgRoleAdmin {
				continue
			}
			orgResource, err := rs.NewResource(
				org.Name,
				resourceTypeOrg,
				org.ID,
				rs.WithParentResourceID(parentResourceID),
				rs.WithAnnotation(
					&v2.ExternalLink{Url: org.URL},
					&v2.V1Identifier{Id: fmt.Sprintf("org:%d", org.ID)},
					&v2.ChildResourceType{ResourceTypeId: resourceTypeUser.Id},
				),
			)
			if err != nil {
				return nil, "", nil, err
			}

			ret = append(ret, orgResource)
		}
	}

	return ret, "", nil, nil
}

func (o *orgResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ *pagination.Token,
) ([]*v2.Entitlement, string, annotations.Annotations, error) {
	rv := make([]*v2.Entitlement, 0, len(orgAccessLevels))
	for _, level := range orgAccessLevels {
		rv = append(rv, entitlement.NewPermissionEntitlement(resource, level,
			entitlement.WithDisplayName(fmt.Sprintf("%s Org %s", resource.DisplayName, titleCase(level))),
			entitlement.WithDescription(fmt.Sprintf("Access to %s org in Github", resource.DisplayName)),
			entitlement.WithAnnotation(&v2.V1Identifier{
				Id: fmt.Sprintf("org:%s:role:%s", resource.Id.Resource, level),
			}),
			entitlement.WithGrantableTo(resourceTypeUser),
		))
	}

	return rv, "", nil, nil
}

func (o *orgResourceType) Grants(ctx context.Context, resource *v2.Resource,
	pToken *pagination.Token) ([]*v2.Grant, string, annotations.Annotations, error) {
	var rv []*v2.Grant
	_, page, err := parsePageToken(pToken.Token, resource.Id)
	if err != nil {
		return nil, "", nil, err
	}

	opts := zendesk.UserListOptions{
		PageOptions: zendesk.PageOptions{
			Page:    page,
			PerPage: pToken.Size,
		},
	}
	users, _, err := o.client.GetOrganizationUsers(ctx, resource.Id, &opts)
	if err != nil {
		return nil, "", nil, fmt.Errorf("zendesk-connector: failed to list org members: %w", err)
	}
	for _, user := range users {
		ur, err := o.userResource(user)
		if err != nil {
			return nil, "", nil, err
		}
		roleName := strings.ToLower(user.Role)
		switch roleName {
		case orgRoleAdmin, orgRoleMember, orgRoleAgent:
			rv = append(rv, grant.NewGrant(resource, roleName, ur.Id, grant.WithAnnotation(&v2.V1Identifier{
				Id: fmt.Sprintf("org-grant:%s:%d:%s", resource.Id.Resource, user.ID, roleName),
			})))
		default:
			ctxzap.Extract(ctx).Warn("Unknown Zendesk Role Name",
				zap.String("role_name", roleName),
				zap.String("zendesk_username", user.Name),
			)
		}
	}

	return rv, "", nil, nil
}

func orgBuilder(client *client.ZendeskClient, orgs []string) *orgResourceType {
	orgMap := make(map[string]struct{})

	for _, o := range orgs {
		orgMap[o] = struct{}{}
	}

	return &orgResourceType{
		resourceType: resourceTypeOrg,
		orgs:         orgMap,
		client:       client,
	}
}

func (o *orgResourceType) userResource(user zendesk.User) (*v2.Resource, error) {
	resource, err := rs.NewUserResource(user.Name, resourceTypeUser, user.ID, nil)
	if err != nil {
		return nil, err
	}

	return resource, nil
}
