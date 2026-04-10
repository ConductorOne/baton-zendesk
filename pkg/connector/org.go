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

type orgResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
	filterToOrgs map[string]struct{}
}

const (
	orgRoleAdmin = "admin"
	orgRoleAgent = "agent"
)

var orgAccessLevels = []string{
	orgRoleAdmin,
	orgRoleAgent,
}

func (o *orgResourceType) ResourceType(_ context.Context) *v2.ResourceType {
	return o.resourceType
}

// List returns all the organizations from the database as resource objects.
func (o *orgResourceType) List(ctx context.Context, parentResourceID *v2.ResourceId, opts rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	var (
		ret []*v2.Resource
		err error
	)

	orgs, nextPageToken, err := o.client.ListOrganizations(ctx, opts.PageToken.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to list organizations: %w", err)
	}

	for _, org := range orgs {
		// If we have a filter, and the org is not in the filter, skip it
		if _, ok := o.filterToOrgs[org.Name]; !ok && len(o.filterToOrgs) > 0 {
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
				&v2.ChildResourceType{ResourceTypeId: resourceTypeTeam.Id},
			),
		)
		if err != nil {
			return nil, nil, err
		}

		ret = append(ret, orgResource)
	}

	return ret, &rs.SyncOpResults{NextPageToken: nextPageToken}, nil
}

func (o *orgResourceType) Entitlements(_ context.Context, resource *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	rv := make([]*v2.Entitlement, 0, len(orgAccessLevels))
	for _, level := range orgAccessLevels {
		rv = append(rv, ent.NewPermissionEntitlement(resource, level,
			ent.WithDisplayName(fmt.Sprintf("%s Organization %s", resource.DisplayName, titleCase(level))),
			ent.WithDescription(fmt.Sprintf("Access to %s organization in Zendesk", resource.DisplayName)),
			ent.WithAnnotation(&v2.V1Identifier{
				Id: fmt.Sprintf("org:%s:role:%s", resource.Id.Resource, level),
			}),
			ent.WithGrantableTo(resourceTypeTeam),
		))
	}

	return rv, nil, nil
}

func (o *orgResourceType) Grants(ctx context.Context, resource *v2.Resource, opts rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	var (
		rv  []*v2.Grant
		err error
	)

	users, nextPageToken, err := o.client.GetOrganizationUsers(ctx, resource.Id, opts.PageToken.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to list org members: %w", err)
	}

	for _, user := range users {
		ur, err := getUserResource(user, resourceTypeTeam)
		if err != nil {
			return nil, nil, err
		}

		roleName := strings.ToLower(user.Role)
		switch roleName {
		case orgRoleAdmin, orgRoleAgent:
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

	return rv, &rs.SyncOpResults{NextPageToken: nextPageToken}, nil
}

func (o *orgResourceType) Grant(ctx context.Context, principal *v2.Resource, entitlement *v2.Entitlement) ([]*v2.Grant, annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)
	if principal.Id.ResourceType != resourceTypeTeam.Id {
		l.Warn(
			"baton-zendesk: only users can be granted organization membership",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, nil, fmt.Errorf("baton-zendesk: only users can be granted organization membership")
	}

	userID, err := strconv.ParseInt(principal.Id.Resource, 10, 64)
	if err != nil {
		return nil, nil, err
	}

	organizationID, err := strconv.ParseInt(entitlement.Resource.Id.Resource, 10, 64)
	if err != nil {
		return nil, nil, err
	}

	organizationMembership := zendesk.OrganizationMembership{
		OrganizationID: organizationID,
		UserID:         userID,
	}
	oganizationMembership, err := o.client.CreateOrganizationMembership(ctx, organizationMembership)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to add user to an organization: %w", err)
	}

	l.Debug("Membership has been created.",
		zap.Int64("ID", oganizationMembership.ID),
		zap.Int64("UserID", oganizationMembership.UserID),
		zap.Int64("OganizationID", oganizationMembership.OrganizationID),
		zap.Time("CreatedAt", oganizationMembership.CreatedAt),
	)

	return nil, nil, nil
}

func (o *orgResourceType) Revoke(ctx context.Context, grant *v2.Grant) (annotations.Annotations, error) {
	l := ctxzap.Extract(ctx)

	entitlement := grant.Entitlement
	principal := grant.Principal

	if principal.Id.ResourceType != resourceTypeTeam.Id {
		l.Warn(
			"baton-zendesk: only users can have organization membership revoked",
			zap.String("principal_type", principal.Id.ResourceType),
			zap.String("principal_id", principal.Id.Resource),
		)
		return nil, fmt.Errorf("baton-zendesk: only users can have organization membership revoked")
	}

	userID, err := strconv.ParseInt(principal.Id.Resource, 10, 64)
	if err != nil {
		return nil, err
	}

	organizationID, err := strconv.ParseInt(entitlement.Resource.Id.Resource, 10, 64)
	if err != nil {
		return nil, err
	}

	organizationMembership := zendesk.OrganizationMembershipListOptions{
		OrganizationID: organizationID,
		UserID:         userID,
	}
	organizationMembershipID, err := o.client.RemoveOrganizationMembershipByID(ctx, organizationMembership)
	if err != nil {
		return nil, fmt.Errorf("baton-zendesk: failed to revoke organization: %w", err)
	}

	l.Debug("Membership has been revoked.",
		zap.String("organizationMembershipID", organizationMembershipID),
	)

	return nil, nil
}

func orgBuilder(c *client.ZendeskClient, orgs []string) *orgResourceType {
	orgMap := make(map[string]struct{})

	for _, o := range orgs {
		orgMap[o] = struct{}{}
	}

	return &orgResourceType{
		resourceType: resourceTypeOrg,
		filterToOrgs: orgMap,
		client:       c,
	}
}
