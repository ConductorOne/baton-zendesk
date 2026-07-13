package connector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
	"github.com/conductorone/baton-sdk/pkg/connectorbuilder"
	"github.com/conductorone/baton-sdk/pkg/pagination"
	"github.com/conductorone/baton-sdk/pkg/types/grant"
	rs "github.com/conductorone/baton-sdk/pkg/types/resource"
	"github.com/conductorone/baton-sdk/pkg/types/sessions"
	"github.com/conductorone/baton-zendesk/pkg/client"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/zap/ctxzap"
	"github.com/nukosuke/go-zendesk/zendesk"
	"go.uber.org/zap"
)

const (
	teamRoleAgent = "agent"
	teamRoleAdmin = "admin"
)

type teamMemberResourceType struct {
	resourceType *v2.ResourceType
	client       *client.ZendeskClient
	// filterToOrgs mirrors org.List's org allow-list so the org grants emitted
	// here stay in scope with the orgs that were actually synced. Empty means
	// "all orgs".
	filterToOrgs map[string]struct{}
}

func (t *teamMemberResourceType) ResourceType(ctx context.Context) *v2.ResourceType {
	return t.resourceType
}

// Team Members are users with the role of "agent" or "admin". users with the role of "end-user" are not team members, but rather customers.
func (t *teamMemberResourceType) List(ctx context.Context, parentResourceID *v2.ResourceId, opts rs.SyncOpAttrs) ([]*v2.Resource, *rs.SyncOpResults, error) {
	bag := &pagination.Bag{}
	if err := bag.Unmarshal(opts.PageToken.Token); err != nil {
		return nil, nil, err
	}
	if bag.Current() == nil {
		bag.Push(pagination.PageState{ResourceTypeID: teamRoleAdmin})
		bag.Push(pagination.PageState{ResourceTypeID: teamRoleAgent})
	}

	users, nextCursor, err := t.client.ListUsers(ctx, bag.ResourceTypeID(), bag.PageToken())
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to list users: %w", err)
	}

	if err := populateCache(ctx, opts.Session, users); err != nil {
		return nil, nil, err
	}

	var rv []*v2.Resource
	for _, user := range users {
		userCopy := user
		userResource, err := getTeamResource(&userCopy, t.resourceType)
		if err != nil {
			return nil, nil, err
		}
		rv = append(rv, userResource)
	}

	if err := bag.Next(nextCursor); err != nil {
		return nil, nil, err
	}
	nextPage, err := bag.Marshal()
	if err != nil {
		return nil, nil, err
	}

	return rv, &rs.SyncOpResults{NextPageToken: nextPage}, nil
}

func (t *teamMemberResourceType) Entitlements(_ context.Context, _ *v2.Resource, _ rs.SyncOpAttrs) ([]*v2.Entitlement, *rs.SyncOpResults, error) {
	return nil, nil, nil
}

// Grants emits this team member's organization membership grants: it lists
// this member's organization memberships and emits an org grant per
// membership. Cost scales with team member count rather than organization
// count.
// resourceTypeOrg skips its own Grants via the SkipGrants annotation; the SDK
// stores these grants against the org resource because grants are keyed by
// their entitlement, not by the resource being synced.
func (t *teamMemberResourceType) Grants(ctx context.Context, resource *v2.Resource, opts rs.SyncOpAttrs) ([]*v2.Grant, *rs.SyncOpResults, error) {
	l := ctxzap.Extract(ctx)

	userID, err := strconv.ParseInt(resource.Id.Resource, 10, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: invalid team member id %q: %w", resource.Id.Resource, err)
	}

	// The organization_membership payload carries no role, and the org
	// entitlement granted (admin vs agent) is keyed on the member's global role,
	// so resolve it from the user cache populated during List.
	roleName, err := t.resolveMemberRole(ctx, opts.Session, userID)
	if err != nil {
		return nil, nil, err
	}
	// org exposes only admin/agent entitlements. End-users are never team
	// members and never reach this path, but guard defensively rather than emit
	// a grant against a nonexistent entitlement.
	if roleName != teamRoleAdmin && roleName != teamRoleAgent {
		l.Warn("baton-zendesk: skipping org grants for team member with unexpected role",
			zap.Int64("user_id", userID),
			zap.String("role", roleName),
		)
		return nil, &rs.SyncOpResults{}, nil
	}

	memberships, nextPageToken, err := t.client.GetUserOrganizationMemberships(ctx, userID, opts.PageToken.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("baton-zendesk: failed to list organization memberships for user %d: %w", userID, err)
	}

	rv := make([]*v2.Grant, 0, len(memberships))
	for _, m := range memberships {
		// Mirror org.List's allow-list: when an org filter is configured, only
		// emit grants for orgs that were actually synced. The membership carries
		// organization_name, the same key org.List filters on, so an out-of-scope
		// org here would otherwise produce a grant against an org (and entitlement)
		// that was never synced.
		if !orgInScope(t.filterToOrgs, m.Name) {
			continue
		}

		orgID := strconv.FormatInt(m.OrganizationID, 10)
		orgResource := &v2.Resource{
			Id: &v2.ResourceId{ResourceType: resourceTypeOrg.Id, Resource: orgID},
		}
		rv = append(rv, grant.NewGrant(orgResource, roleName, resource.Id, grant.WithAnnotation(&v2.V1Identifier{
			Id: fmt.Sprintf("org-grant:%s:%d:%s", orgID, userID, roleName),
		})))
	}

	return rv, &rs.SyncOpResults{NextPageToken: nextPageToken}, nil
}

// resolveMemberRole returns the member's lowercased Zendesk role (admin/agent).
// The organization_membership payload has no role field, so it is read from the
// session user cache populated during List, with a direct user fetch as a
// fallback on a cache miss (mirrors group.Grants, including repopulating the
// cache from the fallback fetch so a repeated miss for the same member doesn't
// cost another API call).
func (t *teamMemberResourceType) resolveMemberRole(ctx context.Context, ss sessions.SessionStore, userID int64) (string, error) {
	if cached, err := getCachedUsersByIDs(ctx, ss, []int64{userID}); err == nil {
		if u, ok := cached[userID]; ok {
			return strings.ToLower(u.Role), nil
		}
	}

	user, err := t.client.GetUser(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("baton-zendesk: failed to resolve role for user %d: %w", userID, err)
	}

	if err := populateCache(ctx, ss, []zendesk.User{user}); err != nil {
		ctxzap.Extract(ctx).Debug("baton-zendesk: failed to populate cache for team member role lookup",
			zap.Int64("user_id", userID),
		)
	}

	return strings.ToLower(user.Role), nil
}

func (t *teamMemberResourceType) CreateAccountCapabilityDetails(ctx context.Context) (*v2.CredentialDetailsAccountProvisioning, annotations.Annotations, error) {
	return &v2.CredentialDetailsAccountProvisioning{
		SupportedCredentialOptions: []v2.CapabilityDetailCredentialOption{
			v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
		},
		PreferredCredentialOption: v2.CapabilityDetailCredentialOption_CAPABILITY_DETAIL_CREDENTIAL_OPTION_NO_PASSWORD,
	}, nil, nil
}

func (t *teamMemberResourceType) CreateAccount(
	ctx context.Context,
	accountInfo *v2.AccountInfo,
	credentialOptions *v2.LocalCredentialOptions,
) (connectorbuilder.CreateAccountResponse, []*v2.PlaintextData, annotations.Annotations, error) {
	pMap := accountInfo.Profile.AsMap()

	name, ok := pMap["name"].(string)
	if !ok || name == "" {
		return nil, nil, nil, fmt.Errorf("name not found in profile")
	}

	newUser := zendesk.User{
		Name: name,
		// role can be "admin", "agent", or "end-user"
		Role: "agent",
	}

	if email, ok := pMap["email"].(string); ok && email != "" {
		newUser.Email = email
	}

	if role, ok := pMap["role"].(string); ok && role != "" {
		newUser.Role = role
	}

	createdUser, err := t.client.CreateUser(ctx, newUser)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create user: %w", err)
	}

	userResource, err := getTeamResource(&createdUser, t.resourceType)
	if err != nil {
		return nil, nil, nil, err
	}

	return &v2.CreateAccountResponse_SuccessResult{
		Resource:              userResource,
		IsCreateAccountResult: true,
	}, nil, nil, nil
}

func (t *teamMemberResourceType) Delete(ctx context.Context, resourceId *v2.ResourceId) (annotations.Annotations, error) {
	userID, err := strconv.ParseInt(resourceId.Resource, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse user ID: %w", err)
	}

	// DeleteUser will return an error when successful (yes, really)
	// see: vendor/github.com/nukosuke/go-zendesk/zendesk/zendesk.go Line:279
	err = t.client.DeleteUser(ctx, userID)
	if err != nil {
		var zErr *zendesk.Error
		if errors.As(err, &zErr) {
			if zErr.Status() == http.StatusUnprocessableEntity {
				return nil, fmt.Errorf("failed to soft delete user %s: %w", resourceId.Resource, err)
			}
		}
	}

	// PermanentlyDeleteUser also returns an error on success
	err = t.client.PermanentlyDeleteUser(ctx, userID)
	if err != nil {
		var zErr *zendesk.Error
		if errors.As(err, &zErr) {
			if zErr.Status() == http.StatusUnprocessableEntity {
				return nil, fmt.Errorf("user %s is not in the deleted state, cannot permanently delete: %w", resourceId.Resource, err)
			}
		}
	}

	return nil, nil
}

func teamMemberBuilder(zendeskClient *client.ZendeskClient, orgs []string) *teamMemberResourceType {
	return &teamMemberResourceType{
		resourceType: resourceTypeTeam,
		client:       zendeskClient,
		filterToOrgs: orgFilterSet(orgs),
	}
}
