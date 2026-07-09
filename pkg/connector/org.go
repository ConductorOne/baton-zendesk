package connector

import (
	"context"
	"fmt"
	"strconv"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	ent "github.com/conductorone/baton-sdk/pkg/types/entitlement"
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
			),
		)
		if err != nil {
			return nil, nil, err
		}

		ret = append(ret, orgResource)
	}

	return ret, &rs.SyncOpResults{NextPageToken: nextPageToken}, nil
}

func (o *orgResourceType) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// StaticEntitlements returns the org access levels as resource-type-level
// entitlement templates. Every organization exposes the same admin/agent
// entitlements, so the SDK materializes these against each org locally instead
// of calling Entitlements() once per org. Paired with the SkipEntitlements
// annotation on resourceTypeOrg, this removes the per-org entitlement fan-out
// that does not scale to tenants with 100k+ organizations.
//
// The materialized entitlement IDs are identical to the per-resource form
// (NewEntitlementID(org, level)), so org.Grants and any existing grants keep
// resolving to the same entitlements. The display name/description are no
// longer prefixed with the org name because a static template applies to every
// org uniformly.
func (o *orgResourceType) StaticEntitlements(_ context.Context, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	rv := make([]*v2.Entitlement, 0, len(orgAccessLevels))
	for _, level := range orgAccessLevels {
		rv = append(rv, ent.NewPermissionEntitlement(nil, level,
			ent.WithDisplayName(fmt.Sprintf("Organization %s", titleCase(level))),
			ent.WithDescription("Access to organization in Zendesk"),
			ent.WithGrantableTo(resourceTypeTeam),
		))
	}

	return rv, nil, nil
}

// Grants is intentionally a no-op. Organization membership grants are emitted
// from teamMemberResourceType.Grants, which inverts the traversal: it iterates
// the small set of team members (agents/admins) and lists each member's
// organization memberships, instead of making role-filtered calls to
// GetOrganizationUsers once per organization. That per-org fan-out does not
// scale to tenants with 100k+ organizations.
//
// resourceTypeOrg carries the SkipGrants annotation, so the SDK never calls
// this during sync; it remains only to satisfy the ResourceSyncer interface.
// The emitted grants still attach to the org resource because the SDK stores
// grants by their entitlement, not by the resource being synced.
func (o *orgResourceType) Grants(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	return nil, nil, nil
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
		if isAlreadyExistsError(err) {
			l.Debug("baton-zendesk: organization membership already exists",
				zap.Int64("user_id", userID),
				zap.Int64("organization_id", organizationID),
			)
			annos := annotations.New()
			annos.Update(&v2.GrantAlreadyExists{})
			return nil, annos, nil
		}
		return nil, nil, fmt.Errorf("baton-zendesk: failed to add user to an organization: %w", err)
	}

	l.Debug("Membership has been created.",
		zap.Int64("ID", oganizationMembership.ID),
		zap.Int64("UserID", oganizationMembership.UserID),
		zap.Int64("OrganizationID", oganizationMembership.OrganizationID),
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
		if isNotFoundError(err) {
			l.Debug("baton-zendesk: organization membership not found; treating revoke as already revoked",
				zap.Int64("user_id", userID),
				zap.Int64("organization_id", organizationID),
			)
			annos := annotations.New()
			annos.Update(&v2.GrantAlreadyRevoked{})
			return annos, nil
		}
		return nil, fmt.Errorf("baton-zendesk: failed to revoke organization: %w", err)
	}

	l.Debug("Membership has been revoked.",
		zap.String("organizationMembershipID", organizationMembershipID),
	)

	return nil, nil
}

func orgBuilder(c *client.ZendeskClient, orgs []string) *orgResourceType {
	return &orgResourceType{
		resourceType: resourceTypeOrg,
		filterToOrgs: orgFilterSet(orgs),
		client:       c,
	}
}
